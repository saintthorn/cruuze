package server

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

func TestMemberRolesAndRemoval(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, bobMe, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	member := func(userID uuid.UUID) string { return base + "/members/" + userID.String() }
	role := func(r api.WorkspaceRole) api.UpdateMemberRequest { return api.UpdateMemberRequest{Role: r} }

	// Only admins change roles, and only to real ones.
	bob.expect(http.StatusForbidden, "PATCH", member(adaMe.User.Id), role(api.WorkspaceRoleGuest), nil)
	ada.expect(http.StatusBadRequest, "PATCH", member(bobMe.User.Id), map[string]any{"role": "owner"}, nil)
	ada.expect(http.StatusNotFound, "PATCH", member(newID()), role(api.WorkspaceRoleMember), nil)

	// The only admin can't step down or leave.
	ada.expect(http.StatusConflict, "PATCH", member(adaMe.User.Id), role(api.WorkspaceRoleMember), nil)
	ada.expect(http.StatusConflict, "DELETE", member(adaMe.User.Id), nil, nil)

	// Becoming a guest changes what bob sees, so his clients start over.
	bob.expect(http.StatusNoContent, "PUT", "/api/teams/"+team.Id.String()+"/members/"+bobMe.User.Id.String(), nil, nil)
	cursor := bob.lastSyncID(ws.Id)
	var ack api.SyncAck
	ada.expect(http.StatusOK, "PATCH", member(bobMe.User.Id), role(api.WorkspaceRoleGuest), &ack)
	if batch := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, cursor); !batch.Reset {
		t.Errorf("bob's socket was not reset when he became a guest: %+v", batch)
	}
	// Everyone else just sees his record change.
	batch := env.fetch(t, ws.Id, adaMe.User, api.WorkspaceRoleAdmin, cursor)
	if batch.Reset || !slices.Equal(models(batch), []string{"U:User"}) || batch.Cursor != ack.SyncId {
		t.Errorf("ada's view of the role change = %v (reset %v)", models(batch), batch.Reset)
	}
	var users []api.UserRecord
	for _, u := range ada.bootstrap(ws.Id).Users {
		if u.Id == bobMe.User.Id {
			users = append(users, u)
		}
	}
	if len(users) != 1 || users[0].Role != api.WorkspaceRoleGuest {
		t.Errorf("bob's record = %+v, want guest", users)
	}
	// The same role again is not a change.
	before := ada.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", member(bobMe.User.Id), role(api.WorkspaceRoleGuest), nil)
	if after := ada.lastSyncID(ws.Id); after != before {
		t.Errorf("setting the same role wrote %d sync actions", after-before)
	}

	// Promoted to admin, bob can demote ada: there is still an admin.
	cursor = bob.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", member(bobMe.User.Id), role(api.WorkspaceRoleAdmin), nil)
	if batch := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleGuest, cursor); !batch.Reset {
		t.Error("bob's socket was not reset when he stopped being a guest")
	}
	bob.expect(http.StatusOK, "PATCH", member(adaMe.User.Id), role(api.WorkspaceRoleMember), nil)
	ada.expect(http.StatusForbidden, "PATCH", member(bobMe.User.Id), role(api.WorkspaceRoleMember), nil)
	ada.expect(http.StatusForbidden, "DELETE", member(bobMe.User.Id), nil, nil)

	// Ada has things of her own in the workspace: a key, a favorite, views.
	var key api.CreatedApiKey
	ada.expect(http.StatusCreated, "POST", base+"/api-keys", api.CreateApiKeyRequest{Name: "CI"}, &key)
	issue := ada.createIssue(team.Id, "Stays behind").Issue
	ada.expect(http.StatusCreated, "POST", base+"/favorites", api.CreateFavoriteRequest{Id: newID(), Kind: api.FavoriteKindIssue, TargetId: issue.Id}, nil)
	ada.expect(http.StatusCreated, "POST", base+"/views", map[string]any{"id": newID(), "name": "Mine", "shared": false, "options": map[string]any{}}, nil)
	ada.expect(http.StatusCreated, "POST", base+"/views", map[string]any{"id": newID(), "name": "Ours", "shared": true, "options": map[string]any{}}, nil)

	// Bob removes her. Her sockets end, her access and her key are gone.
	cursor = ada.lastSyncID(ws.Id)
	bob.expect(http.StatusOK, "DELETE", member(adaMe.User.Id), nil, &ack)
	if batch := env.fetch(t, ws.Id, adaMe.User, api.WorkspaceRoleMember, cursor); !batch.Reset {
		t.Error("ada's socket was not reset when she was removed")
	}
	ada.expect(http.StatusNotFound, "GET", base+"/sync/bootstrap", nil, nil)
	ada.expect(http.StatusNotFound, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"title": "x"}, nil)
	if got := env.client(t).do("GET", "/api/me", nil, nil, "Authorization", "Bearer "+key.Token); got != http.StatusUnauthorized {
		t.Errorf("removed member's API key: status %d, want 401", got)
	}
	var me api.Me
	ada.expect(http.StatusOK, "GET", "/api/me", nil, &me)
	if len(me.Workspaces) != 0 {
		t.Errorf("ada still lists %d workspaces", len(me.Workspaces))
	}
	bob.expect(http.StatusNotFound, "DELETE", member(adaMe.User.Id), nil, nil)

	// What she made for everyone stays; what was hers alone is gone.
	boot := bob.bootstrap(ws.Id)
	if len(boot.Users) != 1 || len(boot.Issues) != 1 || len(boot.Views) != 1 || boot.Views[0].Name != "Ours" {
		t.Errorf("after removal: %d users, %d issues, views %+v", len(boot.Users), len(boot.Issues), boot.Views)
	}
	for _, tm := range boot.TeamMembers {
		if tm.UserId == adaMe.User.Id {
			t.Errorf("ada is still a member of team %s", tm.TeamId)
		}
	}
	// Others saw her leave the team and the workspace.
	batch = env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleAdmin, cursor)
	if !slices.Equal(models(batch), []string{"D:TeamMember", "D:User"}) {
		t.Errorf("bob's view of the removal = %v", models(batch))
	}

	// Invited back, she starts fresh; and a non-admin can leave by herself.
	bob.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "ada@example.com"}, nil)
	ada, _ = env.signIn(t, "ada@example.com")
	if got := ada.bootstrap(ws.Id); len(got.Favorites) != 0 || len(got.Views) != 1 {
		t.Errorf("rejoined: %d favorites, %d views", len(got.Favorites), len(got.Views))
	}
	ada.expect(http.StatusOK, "DELETE", member(adaMe.User.Id), nil, nil)
	ada.expect(http.StatusNotFound, "GET", base+"/sync/bootstrap", nil, nil)
}

func TestRenameWorkspace(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, _ := workspaceWithMember(t, env)
	path := "/api/workspaces/" + ws.Id.String()

	bob.expect(http.StatusForbidden, "PATCH", path, api.UpdateWorkspaceRequest{Name: "Bob's"}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", path, api.UpdateWorkspaceRequest{Name: "  "}, nil)
	var renamed api.Workspace
	ada.expect(http.StatusOK, "PATCH", path, api.UpdateWorkspaceRequest{Name: " Acme Corp "}, &renamed)
	if renamed.Name != "Acme Corp" || renamed.UrlKey != "acme" || renamed.Role != api.WorkspaceRoleAdmin {
		t.Errorf("renamed = %+v", renamed)
	}
	var me api.Me
	bob.expect(http.StatusOK, "GET", "/api/me", nil, &me)
	if me.Workspaces[0].Name != "Acme Corp" {
		t.Errorf("bob sees %q", me.Workspaces[0].Name)
	}
}

func TestTeamPrivacy(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, bobMe, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	teamPath := "/api/teams/" + team.Id.String()
	ada.createIssue(team.Id, "Plans")

	// Bob can see the public team, but isn't in it: he can't close it off.
	admin := api.WorkspaceRoleAdmin
	ada.expect(http.StatusOK, "PATCH", base+"/members/"+bobMe.User.Id.String(), api.UpdateMemberRequest{Role: admin}, nil)
	bob.expect(http.StatusForbidden, "PATCH", teamPath, map[string]any{"private": true}, nil)

	// A member makes it private. Bob's copy is now wrong, so he starts over
	// and no longer gets the team; ada just sees the change.
	cursor := bob.lastSyncID(ws.Id)
	var result api.TeamMutationResult
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"private": true}, &result)
	if !result.Team.Private {
		t.Fatalf("team = %+v, want private", result.Team)
	}
	if batch := env.fetch(t, ws.Id, bobMe.User, admin, cursor); !batch.Reset {
		t.Errorf("bob's socket was not reset when the team went private: %v", models(batch))
	}
	if batch := env.fetch(t, ws.Id, adaMe.User, admin, cursor); batch.Reset || !slices.Equal(models(batch), []string{"U:Team"}) {
		t.Errorf("ada's view = %v (reset %v), want the team update", models(batch), batch.Reset)
	}
	if got := bob.bootstrap(ws.Id); len(got.Teams) != 0 || len(got.Issues) != 0 {
		t.Errorf("bob still gets %d teams and %d issues", len(got.Teams), len(got.Issues))
	}
	bob.expect(http.StatusNotFound, "PATCH", teamPath, map[string]any{"private": false}, nil)

	// Renaming a private team is not a privacy change: nobody is reset.
	cursor = bob.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"name": "Core"}, nil)
	if batch := env.fetch(t, ws.Id, bobMe.User, admin, cursor); batch.Reset || len(batch.Actions) != 0 {
		t.Errorf("bob's view of a private team's rename = %v (reset %v)", models(batch), batch.Reset)
	}

	// Public again: bob starts over once more, and this time gets everything.
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"private": false}, &result)
	if batch := env.fetch(t, ws.Id, bobMe.User, admin, cursor); !batch.Reset {
		t.Error("bob's socket was not reset when the team went public")
	}
	if got := bob.bootstrap(ws.Id); len(got.Teams) != 1 || got.Teams[0].Name != "Core" || len(got.Issues) != 1 {
		t.Errorf("bob gets %d teams and %d issues, want 1 and 1", len(got.Teams), len(got.Issues))
	}

	// Guests outside the team never saw it, so they have nothing to redo.
	guest := api.WorkspaceRoleGuest
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "gus@example.com", Role: &guest}, nil)
	gus, gusMe := env.signIn(t, "gus@example.com")
	cursor = gus.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"private": true}, nil)
	if batch := env.fetch(t, ws.Id, gusMe.User, guest, cursor); batch.Reset || len(batch.Actions) != 0 {
		t.Errorf("guest's view = %v (reset %v), want nothing", models(batch), batch.Reset)
	}
}

func TestIssueDetailsAreLoadedOnDemand(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, team := workspaceWithMember(t, env)

	var created api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/issues",
		api.CreateIssueRequest{Id: newID(), Title: "Discuss", AssigneeId: &bobMe.User.Id}, &created)
	issuePath := "/api/issues/" + created.Issue.Id.String()
	var first, second api.CommentMutationResult
	ada.expect(http.StatusCreated, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "First"}, &first)
	bob.expect(http.StatusCreated, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "Second"}, &second)
	bob.expect(http.StatusOK, "PUT", "/api/comments/"+first.Comment.Id.String()+"/reactions/%F0%9F%91%8D", nil, nil)

	// The bootstrap carries only the comments the inbox quotes: each person
	// was notified about the other's comment.
	commentIDs := func(c *client) []uuid.UUID {
		var ids []uuid.UUID
		for _, comment := range c.bootstrap(ws.Id).Comments {
			ids = append(ids, comment.Id)
		}
		return ids
	}
	if got := commentIDs(ada); !slices.Equal(got, []uuid.UUID{second.Comment.Id}) {
		t.Errorf("ada's bootstrap comments = %v, want only bob's", got)
	}
	if got := commentIDs(bob); !slices.Equal(got, []uuid.UUID{first.Comment.Id}) {
		t.Errorf("bob's bootstrap comments = %v, want only ada's", got)
	}

	// The details have the whole discussion, as of a sync ID.
	details := bob.details(issuePath)
	if len(details.Comments) != 2 || details.Comments[0].Body != "First" || len(details.Reactions) != 1 ||
		len(details.Activities) != 1 || details.Activities[0].Type != api.ActivityTypeCreated {
		t.Errorf("details = %+v", details)
	}
	if want := bob.lastSyncID(ws.Id); details.SyncId != want {
		t.Errorf("details syncId = %d, want %d", details.SyncId, want)
	}

	// A notification doesn't outlive access: once the team is private and
	// bob is out, neither the comment nor the details reach him.
	ada.expect(http.StatusOK, "PATCH", "/api/teams/"+team.Id.String(), map[string]any{"private": true}, nil)
	if got := commentIDs(bob); len(got) != 0 {
		t.Errorf("bob's bootstrap still has comments from a team he can't see: %v", got)
	}
	bob.expect(http.StatusNotFound, "GET", issuePath+"/details", nil, nil)
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", issuePath+"/details", nil, nil)
}

func TestObservability(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	issue := ada.createIssue(ada.bootstrap(ws.Id).Teams[0].Id, "Count me").Issue
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"title": "Counted"}, nil)

	get := func(path string, headers ...string) *http.Response {
		return env.send("GET", path, nil, headers...)
	}

	// Probes.
	if res := get("/healthz"); res.StatusCode != http.StatusNoContent {
		t.Errorf("/healthz: status %d", res.StatusCode)
	}
	if res := get("/readyz"); res.StatusCode != http.StatusNoContent {
		t.Errorf("/readyz: status %d", res.StatusCode)
	}

	// Every response names its request. A proxy's ID is kept; a client's
	// own, with no proxy in front, is not.
	if id := get("/api/auth/config").Header.Get("X-Request-Id"); id == "" {
		t.Error("no X-Request-Id on the response")
	}
	if id := get("/api/auth/config", "X-Request-Id", "from-client").Header.Get("X-Request-Id"); id == "" || id == "from-client" {
		t.Errorf("X-Request-Id = %q, want a fresh one", id)
	}
	env.srv.cfg.TrustProxy = true
	env.handler = env.srv.Handler()
	if id := get("/api/auth/config", "X-Request-Id", "from-proxy").Header.Get("X-Request-Id"); id != "from-proxy" {
		t.Errorf("X-Request-Id = %q, want the incoming one", id)
	}
	env.srv.cfg.TrustProxy = false
	env.handler = env.srv.Handler()

	// API responses stay out of caches, and say which parameter was wrong
	// without the parser's own words.
	res := get("/api/workspaces/not-a-uuid/imports")
	var malformed api.Error
	json.NewDecoder(res.Body).Decode(&malformed) //nolint:errcheck // checked by the fields
	if res.StatusCode != http.StatusBadRequest || malformed.Message != "Invalid parameter: workspaceId." {
		t.Errorf("malformed ID: status %d, message %q", res.StatusCode, malformed.Message)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control on an API response = %q", got)
	}
	if res.Header.Get("Permissions-Policy") == "" {
		t.Error("no Permissions-Policy on the response")
	}

	// Metrics are off without a token, and locked with one.
	if res := get("/metrics"); res.StatusCode != http.StatusNotFound {
		t.Errorf("/metrics without a configured token: status %d, want 404", res.StatusCode)
	}
	env.srv.cfg.MetricsToken = "scrape-me"
	if res := get("/metrics", "Authorization", "Bearer wrong"); res.StatusCode != http.StatusNotFound {
		t.Errorf("/metrics with the wrong token: status %d, want 404", res.StatusCode)
	}
	res = get("/metrics", "Authorization", "Bearer scrape-me")
	body, _ := io.ReadAll(res.Body)
	metrics := string(body)
	for _, want := range []string{
		// By route pattern, not by ID.
		`cruuze_http_requests_total{method="PATCH",route="/api/issues/{issueId}",status="200"} 1`,
		`cruuze_http_request_duration_seconds_count{method="PATCH",route="/api/issues/{issueId}"} 1`,
		`cruuze_http_requests_total{method="GET",route="/readyz",status="204"} 1`,
		"cruuze_sync_sockets 0",
		"cruuze_db_connections_max ",
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
	if res.StatusCode != http.StatusOK || strings.Contains(metrics, issue.Id.String()) {
		t.Errorf("metrics: status %d; must not contain record IDs", res.StatusCode)
	}

	// Large JSON is compressed for clients that accept it.
	req := httptest.NewRequest("GET", "http://app.test/api/workspaces/"+ws.Id.String()+"/sync/bootstrap", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	for name, value := range ada.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("bootstrap Content-Encoding = %q, want gzip", got)
	}
	unzipped, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var boot api.SyncBootstrap
	if err := json.NewDecoder(unzipped).Decode(&boot); err != nil || len(boot.Issues) != 1 {
		t.Errorf("compressed bootstrap: %v, %d issues", err, len(boot.Issues))
	}

	// A panic becomes a plain 500.
	rec = httptest.NewRecorder()
	env.srv.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })).
		ServeHTTP(rec, httptest.NewRequest("GET", "http://app.test/api/x", nil))
	var apiErr api.Error
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil || rec.Code != http.StatusInternalServerError || apiErr.Code != "internal" || strings.Contains(apiErr.Message, "boom") {
		t.Errorf("panic response: status %d, body %+v (%v)", rec.Code, apiErr, err)
	}
}

func TestRateLimit(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	bob, _ := env.signIn(t, "bob@example.com")

	env.srv.cfg.RateLimit = 3
	env.handler = env.srv.Handler()
	for range 3 {
		ada.expect(http.StatusOK, "GET", "/api/me", nil, nil)
	}
	ada.expect(http.StatusTooManyRequests, "GET", "/api/me", nil, nil)
	// Counted per signed-in client, not per address: bob is unaffected.
	bob.expect(http.StatusOK, "GET", "/api/me", nil, nil)
	// Probes aren't API calls.
	if res := env.send("GET", "/healthz", nil); res.StatusCode != http.StatusNoContent {
		t.Errorf("/healthz while limited: status %d", res.StatusCode)
	}
	// A made-up credential on each request doesn't buy a fresh budget: those
	// are counted by address.
	for i := range 4 {
		want := http.StatusUnauthorized
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if res := env.send("GET", "/api/me", nil, "Authorization", fmt.Sprintf("Bearer made-up-%d", i)); res.StatusCode != want {
			t.Errorf("made-up key %d: status %d, want %d", i, res.StatusCode, want)
		}
	}
}

func TestClientIP(t *testing.T) {
	cases := map[string]string{
		"203.0.113.9:4321":   "203.0.113.9",
		"[2001:db8::1]:4321": "2001:db8::1",
		"203.0.113.9":        "203.0.113.9", // as set from X-Real-IP
	}
	for remote, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if got := clientIP(r); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", remote, got, want)
		}
	}
}
