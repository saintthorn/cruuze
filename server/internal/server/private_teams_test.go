package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
)

// twoAdmins is a workspace run by ada and bob, with a private team only ada
// is in.
func twoAdmins(t *testing.T, env *testEnv) (ada, bob *client, bobMe api.Me, ws api.Workspace, secret api.Team) {
	t.Helper()
	ada, bob, _, bobMe, ws, _ = workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	ada.expect(http.StatusOK, "PATCH", base+"/members/"+bobMe.User.Id.String(), api.UpdateMemberRequest{Role: api.WorkspaceRoleAdmin}, nil)
	private := true
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)
	return ada, bob, bobMe, ws, secret
}

func TestPrivateTeamWebhooksStayWithItsAdmins(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, ws, secret := twoAdmins(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	receiver := &endpoint{status: http.StatusOK}
	env.srv.outbound = &http.Client{Transport: receiver}

	// An admin outside a private team can't see or change its webhook.
	var hook api.Webhook
	ada.expect(http.StatusCreated, "POST", base+"/webhooks", api.CreateWebhookRequest{Url: "https://hooks.example.com/secret", TeamId: &secret.Id}, &hook)
	var listed []api.Webhook
	bob.expect(http.StatusOK, "GET", base+"/webhooks", nil, &listed)
	if len(listed) != 0 {
		t.Errorf("bob sees the private team's webhook: %+v", listed)
	}
	hookPath := "/api/webhooks/" + hook.Id.String()
	elsewhere := "https://hooks.example.com/bob"
	bob.expect(http.StatusNotFound, "PATCH", hookPath, api.UpdateWebhookRequest{Url: &elsewhere}, nil)
	bob.expect(http.StatusNotFound, "GET", hookPath+"/deliveries", nil, nil)
	bob.expect(http.StatusNotFound, "DELETE", hookPath, nil, nil)
	ada.expect(http.StatusOK, "GET", base+"/webhooks", nil, &listed)
	if len(listed) != 1 {
		t.Errorf("ada sees %d webhooks, want 1", len(listed))
	}
	ada.expect(http.StatusNoContent, "DELETE", hookPath, nil, nil)

	// A webhook set up on a public team stops when the team goes private
	// without the person who set it up.
	var open api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Open", Key: "OPN"}, &open)
	bob.expect(http.StatusCreated, "POST", base+"/webhooks", api.CreateWebhookRequest{Url: "https://hooks.example.com/bob", TeamId: &open.Id}, &hook)
	ada.createIssue(open.Id, "For all to see")
	if n := env.deliver(t); n == 0 {
		t.Fatal("nothing delivered for a public team")
	}
	receiver.take(t)
	ada.expect(http.StatusOK, "PATCH", "/api/teams/"+open.Id.String(), map[string]any{"private": true}, nil)
	ada.createIssue(open.Id, "Not for bob")
	if n := env.deliver(t); n != 0 {
		t.Errorf("delivered %d changes of a team that went private to an outsider's webhook", n)
	}
	// An admin in the team takes it over by changing it.
	label := "Ours now"
	ada.expect(http.StatusOK, "PATCH", "/api/webhooks/"+hook.Id.String(), api.UpdateWebhookRequest{Label: &label}, nil)
	ada.createIssue(open.Id, "For the team")
	if n := env.deliver(t); n == 0 {
		t.Error("nothing delivered after a team member took the webhook over")
	}
}

func TestGuestsDontAddTeamMembers(t *testing.T) {
	env := newTestEnv(t)
	ada, _, bobMe, ws, secret := twoAdmins(t, env)
	guest := api.WorkspaceRoleGuest
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/invites", api.CreateInviteRequest{Email: "gus@example.com", Role: &guest}, nil)
	gus, gusMe := env.signIn(t, "gus@example.com")
	members := "/api/teams/" + secret.Id.String() + "/members/"
	ada.expect(http.StatusNoContent, "PUT", members+gusMe.User.Id.String(), nil, nil)
	gus.expect(http.StatusForbidden, "PUT", members+bobMe.User.Id.String(), nil, nil)
}

func TestImportsLeavePrivateTeamsAlone(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, ws, secret := twoAdmins(t, env)
	importsPath := "/api/workspaces/" + ws.Id.String() + "/imports"
	bundle := sampleBundle(time.Now().UTC().Truncate(time.Second))
	bundle["teams"].([]map[string]any)[0]["key"] = "SEC"
	request := map[string]any{"source": "bundle", "bundle": bundle}

	// The preview doesn't offer a team the admin can't see.
	var preview api.ImportPreview
	bob.expect(http.StatusOK, "POST", importsPath+"/preview", request, &preview)
	if len(preview.Teams) != 1 || preview.Teams[0].TeamId != nil {
		t.Errorf("preview for an outsider = %+v", preview.Teams)
	}
	ada.expect(http.StatusOK, "POST", importsPath+"/preview", request, &preview)
	if len(preview.Teams) != 1 || preview.Teams[0].TeamId == nil || *preview.Teams[0].TeamId != secret.Id {
		t.Errorf("preview for a member = %+v", preview.Teams)
	}

	// Nor does the import go there, by its key or by choice.
	if done := bob.runImport(ws.Id, request); done.Status != api.ImportStatusFailed {
		t.Errorf("import matching a private team's key: status %s", done.Status)
	}
	request["teamMap"] = map[string]string{"t1": secret.Id.String()}
	if done := bob.runImport(ws.Id, request); done.Status != api.ImportStatusFailed {
		t.Errorf("import sent to a private team: status %s", done.Status)
	}
	for _, issue := range ada.bootstrap(ws.Id).Issues {
		if issue.TeamId == secret.Id {
			t.Fatalf("an outsider's import wrote %q into the private team", issue.Title)
		}
	}
	if done := ada.runImport(ws.Id, request); done.Status != api.ImportStatusDone {
		t.Errorf("a member's import into the private team: status %s (%s)", done.Status, done.Error)
	}
}

func TestSyncStartsOverAfterAccessChanged(t *testing.T) {
	env := newTestEnv(t)
	ada, _, _, ws, _ := twoAdmins(t, env)
	ctx := context.Background()
	var open api.Team
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/teams", api.CreateTeamRequest{Name: "Open", Key: "OPN"}, &open)
	ada.createIssue(open.Id, "Public for now")
	cursor := ada.lastSyncID(ws.Id)

	changed := func(after int64) bool {
		t.Helper()
		got, err := env.q.HasSyncResetAfter(ctx, dbgen.HasSyncResetAfterParams{WorkspaceID: ws.Id, SyncID: after})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if changed(cursor) {
		t.Fatal("a reset is pending with nothing changed")
	}
	// A client that far back would be replayed what the team held while it
	// was public; it has to start over instead.
	ada.expect(http.StatusOK, "PATCH", "/api/teams/"+open.Id.String(), map[string]any{"private": true}, nil)
	if !changed(cursor) {
		t.Error("no reset for a cursor from before the team went private")
	}
	if changed(ada.lastSyncID(ws.Id)) {
		t.Error("a reset is pending for a client that is up to date")
	}
}

func TestApiKeyLeavesOtherWorkspacesTimerRunning(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	here := ada.createWorkspace("Here", "here")
	there := ada.createWorkspace("There", "there")
	hereIssue := ada.createIssue(ada.bootstrap(here.Id).Teams[0].Id, "With the key").Issue
	thereIssue := ada.createIssue(ada.bootstrap(there.Id).Teams[0].Id, "Being timed").Issue
	now := time.Now().UTC().Truncate(time.Second)
	entries := func(issueID uuid.UUID) string { return "/api/issues/" + issueID.String() + "/time-entries" }

	ada.expect(http.StatusCreated, "POST", entries(thereIssue.Id), map[string]any{"id": uuid.New(), "startedAt": now.Add(-time.Minute)}, nil)
	var key api.CreatedApiKey
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+here.Id.String()+"/api-keys", api.CreateApiKeyRequest{Name: "CI"}, &key)

	body, _ := json.Marshal(map[string]any{"id": uuid.New(), "startedAt": now})
	res := env.send("POST", entries(hereIssue.Id), body, "Authorization", "Bearer "+key.Token, "Content-Type", "application/json")
	if res.StatusCode != http.StatusConflict {
		t.Errorf("starting a timer with a key while one runs elsewhere: status %d, want 409", res.StatusCode)
	}
	if running := ada.timeEntries(there.Id); len(running) != 1 || running[0].EndedAt != nil {
		t.Errorf("the other workspace's timer = %+v, want it still running", running)
	}
	// Signed in, starting one stops the other as before.
	ada.expect(http.StatusCreated, "POST", entries(hereIssue.Id), map[string]any{"id": uuid.New(), "startedAt": now}, nil)
	if stopped := ada.timeEntries(there.Id); len(stopped) != 1 || stopped[0].EndedAt == nil {
		t.Errorf("the other workspace's timer = %+v, want it stopped", stopped)
	}
}

func TestNoEmailAboutATeamSinceLeft(t *testing.T) {
	env := newTestEnv(t)
	ada, _, bobMe, _, secret := twoAdmins(t, env)
	bobInTeam := "/api/teams/" + secret.Id.String() + "/members/" + bobMe.User.Id.String()
	ada.expect(http.StatusNoContent, "PUT", bobInTeam, nil, nil)
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+secret.Id.String()+"/issues",
		api.CreateIssueRequest{Id: newID(), Title: "Hush", AssigneeId: &bobMe.User.Id}, nil)
	ada.expect(http.StatusNoContent, "DELETE", bobInTeam, nil, nil)

	n, err := env.srv.sendNotificationEmails(context.Background(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sent %d emails about a private team the person has left: %q", n, env.mail.lastTo(t, "bob@example.com").Subject)
	}
}
