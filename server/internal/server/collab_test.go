package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
)

// raw sends a request with the client's cookies and returns the response.
func (c *client) raw(method, path string, body io.Reader, contentType string) *http.Response {
	c.t.Helper()
	req := httptest.NewRequest(method, "http://app.test"+path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	c.env.handler.ServeHTTP(rec, req)
	return rec.Result()
}

// upload posts a file to an issue and returns the response.
func (c *client) upload(issueID uuid.UUID, filename string, content []byte) *http.Response {
	c.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		c.t.Fatal(err)
	}
	part.Write(content) //nolint:errcheck // bytes.Buffer
	form.Close()
	return c.raw("POST", "/api/issues/"+issueID.String()+"/attachments", &body, form.FormDataContentType())
}

func mention(user api.User) string {
	return "[@" + user.Name + "](mention://user/" + user.Id.String() + ")"
}

// details loads an issue's comments, reactions and activity. issuePath is
// the issue's API path, /api/issues/{id}.
func (c *client) details(issuePath string) api.IssueDetails {
	c.t.Helper()
	var out api.IssueDetails
	c.expect(http.StatusOK, "GET", issuePath+"/details", nil, &out)
	return out
}

// notificationTypes lists a user's notifications, oldest first.
func (c *client) notificationTypes(workspaceID uuid.UUID) []api.NotificationType {
	c.t.Helper()
	notifications := c.bootstrap(workspaceID).Notifications
	out := make([]api.NotificationType, len(notifications))
	for i, n := range notifications {
		out[len(notifications)-1-i] = n.Type
	}
	return out
}

func sameTypes(got []api.NotificationType, want ...api.NotificationType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCommentsAndNotifications(t *testing.T) {
	env := newTestEnv(t)
	ada, adaMe := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	base := "/api/workspaces/" + ws.Id.String()
	team := ada.bootstrap(ws.Id).Teams[0]
	states := ada.bootstrap(ws.Id).WorkflowStates

	member := api.WorkspaceRoleMember
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "bob@example.com", Role: &member}, nil)
	bob, bobMe := env.signIn(t, "bob@example.com")
	bob.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"name": "Bob"}, &bobMe.User)

	// Assigning someone subscribes and notifies them; the creator follows too.
	var created api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/issues",
		api.CreateIssueRequest{Id: newID(), Title: "Ship it", AssigneeId: &bobMe.User.Id}, &created)
	issue := created.Issue
	if len(issue.SubscriberIds) != 2 {
		t.Errorf("subscribers = %v, want the creator and the assignee", issue.SubscriberIds)
	}
	if got := bob.notificationTypes(ws.Id); !sameTypes(got, api.NotificationTypeAssigned) {
		t.Errorf("bob's notifications = %v, want [assigned]", got)
	}
	if got := ada.notificationTypes(ws.Id); len(got) != 0 {
		t.Errorf("ada was notified of her own action: %v", got)
	}
	issuePath := "/api/issues/" + issue.Id.String()

	// A comment that mentions Ada: she hears "mentioned", not "commented".
	bobComment := api.CreateCommentRequest{Id: newID(), Body: mention(adaMe.User) + " take a look"}
	var first, retried api.CommentMutationResult
	bob.expect(http.StatusCreated, "POST", issuePath+"/comments", bobComment, &first)
	bob.expect(http.StatusOK, "POST", issuePath+"/comments", bobComment, &retried)
	if retried.Comment.Id != first.Comment.Id || first.Comment.UserId == nil || *first.Comment.UserId != bobMe.User.Id {
		t.Errorf("comment = %+v, retried = %+v", first.Comment, retried.Comment)
	}
	if got := ada.notificationTypes(ws.Id); !sameTypes(got, api.NotificationTypeMentioned) {
		t.Errorf("ada's notifications = %v, want [mentioned]", got)
	}
	// A plain comment from Ada reaches Bob as a follower.
	var second api.CommentMutationResult
	ada.expect(http.StatusCreated, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "On it."}, &second)
	if got := bob.notificationTypes(ws.Id); !sameTypes(got, api.NotificationTypeAssigned, api.NotificationTypeCommented) {
		t.Errorf("bob's notifications = %v, want [assigned commented]", got)
	}
	ada.expect(http.StatusBadRequest, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "  "}, nil)

	// Only the author edits; the author or an admin deletes.
	adaComment := "/api/comments/" + second.Comment.Id.String()
	bob.expect(http.StatusForbidden, "PATCH", adaComment, api.UpdateCommentRequest{Body: "hijacked"}, nil)
	bob.expect(http.StatusForbidden, "DELETE", adaComment, nil, nil)
	var edited api.CommentMutationResult
	ada.expect(http.StatusOK, "PATCH", adaComment, api.UpdateCommentRequest{Body: "On it now."}, &edited)
	if edited.Comment.Body != "On it now." || edited.Comment.EditedAt == nil {
		t.Errorf("edited comment = %+v", edited.Comment)
	}

	// Reactions: one per person and emoji, from a fixed set.
	reaction := adaComment + "/reactions/" + "%F0%9F%91%8D" // 👍
	bob.expect(http.StatusOK, "PUT", reaction, nil, nil)
	bob.expect(http.StatusOK, "PUT", reaction, nil, nil)
	ada.expect(http.StatusOK, "PUT", reaction, nil, nil)
	if got := ada.details(issuePath).Reactions; len(got) != 2 || got[0].Emoji != "👍" {
		t.Errorf("reactions = %+v, want one 👍 each from bob and ada", got)
	}
	bob.expect(http.StatusBadRequest, "PUT", adaComment+"/reactions/nope", nil, nil)
	bob.expect(http.StatusOK, "DELETE", reaction, nil, nil)
	if got := ada.details(issuePath).Reactions; len(got) != 1 {
		t.Errorf("%d reactions after removing one, want 1", len(got))
	}

	// A status change notifies followers and lands in the activity feed.
	done := states[3]
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"stateId": done.Id, "title": "Ship it today"}, nil)
	if got := bob.notificationTypes(ws.Id); !sameTypes(got, api.NotificationTypeAssigned, api.NotificationTypeCommented, api.NotificationTypeStatus) {
		t.Errorf("bob's notifications = %v, want [assigned commented status]", got)
	}
	var kinds []api.ActivityType
	var stateChange api.Activity
	for _, a := range ada.details(issuePath).Activities {
		kinds = append(kinds, a.Type)
		if a.Type == api.ActivityTypeState {
			stateChange = a
		}
	}
	if len(kinds) != 3 || kinds[0] != api.ActivityTypeCreated {
		t.Errorf("activities = %v, want created, then title and state", kinds)
	}
	if stateChange.To == nil || *stateChange.To != done.Id.String() || stateChange.ActorId == nil || *stateChange.ActorId != adaMe.User.Id {
		t.Errorf("state activity = %+v", stateChange)
	}
	// Asking for what is already so changes nothing and logs nothing.
	before := ada.bootstrap(ws.Id).LastSyncId
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"stateId": done.Id}, nil)
	if after := ada.bootstrap(ws.Id).LastSyncId; after != before {
		t.Errorf("a no-op update moved the sync ID from %d to %d", before, after)
	}

	// Notifications are private: they aren't in anyone else's log or reach.
	adaLog := env.fetch(t, ws.Id, adaMe.User, api.WorkspaceRoleAdmin, 0)
	for _, a := range adaLog.Actions {
		if a.Model == string(api.SyncModelNotification) && !strings.Contains(string(a.Data), string(api.NotificationTypeMentioned)) {
			t.Errorf("ada's log contains someone else's notification: %s", a.Data)
		}
	}
	bobNotifications := bob.bootstrap(ws.Id).Notifications
	theirs := "/api/notifications/" + bobNotifications[0].Id.String()
	ada.expect(http.StatusNotFound, "PATCH", theirs, api.UpdateNotificationRequest{Read: true}, nil)
	ada.expect(http.StatusNotFound, "DELETE", theirs, nil, nil)
	bob.expect(http.StatusOK, "PATCH", theirs, api.UpdateNotificationRequest{Read: true}, nil)
	if got := bob.bootstrap(ws.Id).Notifications[0]; got.ReadAt == nil {
		t.Error("notification not marked read")
	}
	bob.expect(http.StatusOK, "PATCH", theirs, api.UpdateNotificationRequest{Read: false}, nil)
	bob.expect(http.StatusOK, "DELETE", theirs, nil, nil)
	if got := bob.bootstrap(ws.Id).Notifications; len(got) != 2 {
		t.Errorf("%d notifications after deleting one, want 2", len(got))
	}

	// Unsubscribing stops the notifications.
	var unsubscribed api.IssueMutationResult
	bob.expect(http.StatusOK, "DELETE", issuePath+"/subscription", nil, &unsubscribed)
	if len(unsubscribed.Issue.SubscriberIds) != 1 {
		t.Errorf("subscribers after unsubscribing = %v", unsubscribed.Issue.SubscriberIds)
	}
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"stateId": states[0].Id}, nil)
	if got := bob.bootstrap(ws.Id).Notifications; len(got) != 2 {
		t.Errorf("an unsubscribed user was notified: %d notifications", len(got))
	}
	bob.expect(http.StatusOK, "PUT", issuePath+"/subscription", nil, nil)

	// An admin can remove anyone's comment.
	var ack api.SyncAck
	ada.expect(http.StatusOK, "DELETE", "/api/comments/"+first.Comment.Id.String(), nil, &ack)
	if got := ada.details(issuePath).Comments; len(got) != 1 {
		t.Errorf("%d comments after deleting one, want 1", len(got))
	}

	// Mentions never reach people who can't see the issue.
	private := true
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)
	eve, eveMe := env.signIn(t, "eve@example.com")
	description := "cc " + mention(bobMe.User) + " and " + mention(eveMe.User)
	var hidden api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+secret.Id.String()+"/issues",
		api.CreateIssueRequest{Id: newID(), Title: "Secret plan", Description: &description}, &hidden)
	if len(hidden.Issue.SubscriberIds) != 1 {
		t.Errorf("a private issue's subscribers = %v, want only its creator", hidden.Issue.SubscriberIds)
	}
	if got := bob.bootstrap(ws.Id).Notifications; len(got) != 2 {
		t.Errorf("bob was notified about an issue he can't see: %d notifications", len(got))
	}
	eve.expect(http.StatusNotFound, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "hi"}, nil)
	eve.expect(http.StatusNotFound, "PUT", reaction, nil, nil)
	eve.expect(http.StatusNotFound, "PUT", issuePath+"/subscription", nil, nil)
}

func TestAttachments(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	base := "/api/workspaces/" + ws.Id.String()
	team := ada.bootstrap(ws.Id).Teams[0]
	issue := ada.createIssue(team.Id, "With files").Issue

	member := api.WorkspaceRoleMember
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "bob@example.com", Role: &member}, nil)
	bob, _ := env.signIn(t, "bob@example.com")
	eve, _ := env.signIn(t, "eve@example.com")

	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	res := ada.upload(issue.Id, "../../shot.png", png)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload: status %d", res.StatusCode)
	}
	var uploaded api.AttachmentMutationResult
	decode(t, res, &uploaded)
	att := uploaded.Attachment
	if att.Filename != "shot.png" || att.ContentType != "image/png" || att.Size != int64(len(png)) {
		t.Errorf("attachment = %+v", att)
	}
	if got := ada.bootstrap(ws.Id).Attachments; len(got) != 1 {
		t.Errorf("%d attachments in bootstrap, want 1", len(got))
	}

	// Images are shown inline, locked down so they can't act as a page.
	path := "/api/attachments/" + att.Id.String()
	res = bob.raw("GET", path, nil, "")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, png) {
		t.Fatalf("download: status %d, %d bytes", res.StatusCode, len(body))
	}
	if d := res.Header.Get("Content-Disposition"); !strings.HasPrefix(d, "inline") {
		t.Errorf("image disposition = %q, want inline", d)
	}
	if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("download is missing its lockdown headers: %v", res.Header)
	}

	// Anything a browser could run is downloaded, whatever it is named.
	res = ada.upload(issue.Id, "innocent.png", []byte("<html><script>alert(1)</script></html>"))
	var html api.AttachmentMutationResult
	decode(t, res, &html)
	res = ada.raw("GET", "/api/attachments/"+html.Attachment.Id.String(), nil, "")
	if d := res.Header.Get("Content-Disposition"); !strings.HasPrefix(d, "attachment") {
		t.Errorf("HTML disposition = %q (type %q), want attachment", d, html.Attachment.ContentType)
	}

	if res := ada.upload(issue.Id, "empty.txt", nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("empty upload: status %d, want 400", res.StatusCode)
	}

	// Outsiders see nothing; only the uploader or an admin deletes.
	if res := eve.raw("GET", path, nil, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("outsider download: status %d, want 404", res.StatusCode)
	}
	if res := eve.upload(issue.Id, "x.txt", []byte("x")); res.StatusCode != http.StatusNotFound {
		t.Errorf("outsider upload: status %d, want 404", res.StatusCode)
	}
	bob.expect(http.StatusForbidden, "DELETE", path, nil, nil)
	ada.expect(http.StatusOK, "DELETE", path, nil, nil)
	if res := ada.raw("GET", path, nil, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("download after delete: status %d, want 404", res.StatusCode)
	}
}

func decode(t *testing.T, res *http.Response, out any) {
	t.Helper()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatalf("decode response (status %d): %v", res.StatusCode, err)
	}
}

func TestMentionedUsers(t *testing.T) {
	a, b := newID(), newID()
	text := "Hi [@Ada](mention://user/" + a.String() + "), [@Bob](mention://user/" + b.String() + ") and again [@Ada](mention://user/" + a.String() + "). " +
		"Not a mention: [link](https://example.com/mention://user/x) or mention://user/" + newID().String()
	got := mentionedUsers(text)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("mentionedUsers = %v, want [%v %v]", got, a, b)
	}
	if got := mentionedUsers("no mentions here"); len(got) != 0 {
		t.Errorf("mentionedUsers = %v, want none", got)
	}
}

func TestIssueChanges(t *testing.T) {
	x, y, label := newID(), newID(), newID()
	before := dbgen.Issue{Title: "Old", StateID: x, Priority: 0, LabelIds: []uuid.UUID{label}, SortOrder: 1}
	after := dbgen.Issue{Title: "New", StateID: y, Priority: 2, AssigneeID: &x, SortOrder: 9}

	var kinds []api.ActivityType
	for _, change := range issueChanges(before, after) {
		kinds = append(kinds, change.kind)
	}
	want := []api.ActivityType{api.ActivityTypeTitle, api.ActivityTypeState, api.ActivityTypePriority, api.ActivityTypeAssignee, api.ActivityTypeLabels}
	if len(kinds) != len(want) {
		t.Fatalf("changes = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("change %d = %s, want %s", i, kinds[i], want[i])
		}
	}
	// Reordering alone is not an activity.
	if got := issueChanges(before, dbgen.Issue{Title: "Old", StateID: x, LabelIds: []uuid.UUID{label}, SortOrder: 5}); len(got) != 0 {
		t.Errorf("reordering produced %d activities", len(got))
	}
}

func TestCleanFilename(t *testing.T) {
	cases := map[string]string{
		"report.pdf":           "report.pdf",
		"../../etc/passwd":     "passwd",
		`C:\Users\ada\cat.png`: "cat.png",
		"line\r\nbreak.txt":    "linebreak.txt",
		"":                     "file",
		"..":                   "file",
		"   ":                  "file",
	}
	for in, want := range cases {
		if got := cleanFilename(in); got != want {
			t.Errorf("cleanFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanFilename(strings.Repeat("a", 500) + ".png"); len([]rune(got)) != maxFilenameLen || !strings.HasSuffix(got, ".png") {
		t.Errorf("long name was cut to %d runes ending %q", len([]rune(got)), got[len(got)-4:])
	}
}
