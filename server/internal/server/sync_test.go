package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

func (c *client) bootstrap(workspaceID uuid.UUID) api.SyncBootstrap {
	c.t.Helper()
	var out api.SyncBootstrap
	c.expect(http.StatusOK, "GET", "/api/workspaces/"+workspaceID.String()+"/sync/bootstrap", nil, &out)
	return out
}

func (c *client) createIssue(teamID uuid.UUID, title string) api.IssueMutationResult {
	c.t.Helper()
	var out api.IssueMutationResult
	req := api.CreateIssueRequest{Id: newID(), Title: title}
	c.expect(http.StatusCreated, "POST", "/api/teams/"+teamID.String()+"/issues", req, &out)
	return out
}

// fetch reads the sync log the way a user's socket would.
func (e *testEnv) fetch(t *testing.T, workspaceID uuid.UUID, user api.User, role api.WorkspaceRole, after int64) syncer.Batch {
	t.Helper()
	ctx := context.Background()
	access, err := syncer.LoadAccess(ctx, e.q, workspaceID, user.Id, string(role))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := syncer.Fetch(ctx, e.q, workspaceID, access, after, 500)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func models(batch syncer.Batch) []string {
	out := make([]string, len(batch.Actions))
	for i, a := range batch.Actions {
		out[i] = a.Action + ":" + a.Model
	}
	return out
}

// only keeps the actions on one model, e.g. to ignore the activity entries
// that accompany issue changes.
func only(batch syncer.Batch, model api.SyncModel) []string {
	var out []string
	for _, a := range batch.Actions {
		if a.Model == string(model) {
			out = append(out, a.Action+":"+a.Model)
		}
	}
	return out
}

// lastSyncID is how far the workspace's log reaches.
func (c *client) lastSyncID(workspaceID uuid.UUID) int64 {
	c.t.Helper()
	return c.bootstrap(workspaceID).LastSyncId
}

func TestBootstrapAndIssueSync(t *testing.T) {
	env := newTestEnv(t)
	ada, adaMe := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")

	boot := ada.bootstrap(ws.Id)
	if len(boot.Users) != 1 || len(boot.Teams) != 1 || len(boot.TeamMembers) != 1 || len(boot.WorkflowStates) != 5 || len(boot.Issues) != 0 {
		t.Fatalf("bootstrap = %d users, %d teams, %d team members, %d states, %d issues; want 1, 1, 1, 5, 0",
			len(boot.Users), len(boot.Teams), len(boot.TeamMembers), len(boot.WorkflowStates), len(boot.Issues))
	}
	// Creating the workspace logged the user, the team, its states and the membership.
	if boot.LastSyncId != 8 {
		t.Errorf("lastSyncId = %d, want 8", boot.LastSyncId)
	}
	team := boot.Teams[0]
	issuesPath := "/api/teams/" + team.Id.String() + "/issues"

	// Create: lands in the backlog, numbered from 1, and is the next sync ID.
	req := api.CreateIssueRequest{Id: newID(), Title: "  Fix login  "}
	var created api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", issuesPath, req, &created)
	if created.Issue.Number != 1 || created.Issue.Title != "Fix login" || created.SyncId != boot.LastSyncId+1 {
		t.Errorf("created = %+v", created)
	}
	if created.Issue.StateId != boot.WorkflowStates[0].Id || boot.WorkflowStates[0].Type != api.WorkflowStateTypeBacklog {
		t.Errorf("new issue state = %s, want the backlog state %s", created.Issue.StateId, boot.WorkflowStates[0].Id)
	}
	if created.Issue.CreatorId == nil || *created.Issue.CreatorId != adaMe.User.Id {
		t.Errorf("creator = %v, want ada", created.Issue.CreatorId)
	}

	// Retrying the create returns the same issue instead of a duplicate.
	var retried api.IssueMutationResult
	ada.expect(http.StatusOK, "POST", issuesPath, req, &retried)
	if retried.Issue.Number != 1 || retried.SyncId < created.SyncId {
		t.Errorf("retried = %+v", retried)
	}

	second := ada.createIssue(team.Id, "Second")
	if second.Issue.Number != 2 || second.Issue.SortOrder >= created.Issue.SortOrder {
		t.Errorf("second issue number %d sort %v; want number 2 sorted above the first (%v)",
			second.Issue.Number, second.Issue.SortOrder, created.Issue.SortOrder)
	}

	// Update only what is sent.
	issuePath := "/api/issues/" + created.Issue.Id.String()
	done := boot.WorkflowStates[3].Id
	var updated api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"stateId": done, "assigneeId": adaMe.User.Id, "priority": 2}, &updated)
	if updated.Issue.StateId != done || updated.Issue.Title != "Fix login" || updated.Issue.Priority != 2 ||
		updated.Issue.AssigneeId == nil || *updated.Issue.AssigneeId != adaMe.User.Id {
		t.Errorf("updated = %+v", updated.Issue)
	}
	// Null clears the assignee; leaving it out keeps it.
	// Each response gets a fresh variable: omitted fields would otherwise
	// keep their value from the previous decode.
	var retitled, unassigned, noop api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"title": "Fix sign-in"}, &retitled)
	if retitled.Issue.AssigneeId == nil {
		t.Error("assignee was cleared by an update that didn't mention it")
	}
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"assigneeId": nil}, &unassigned)
	if unassigned.Issue.AssigneeId != nil || unassigned.Issue.Title != "Fix sign-in" {
		t.Errorf("after unassign = %+v", unassigned.Issue)
	}
	// An empty update changes nothing and logs nothing.
	logged := ada.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{}, &noop)
	if after := ada.lastSyncID(ws.Id); after != logged || noop.SyncId != logged {
		t.Errorf("empty update moved the log from %d to %d (reported %d)", logged, after, noop.SyncId)
	}

	// Validation.
	otherWs := ada.createWorkspace("Other", "other")
	foreign := ada.bootstrap(otherWs.Id)
	for name, body := range map[string]map[string]any{
		"empty title":         {"title": "   "},
		"priority range":      {"priority": 9},
		"state of other team": {"stateId": foreign.WorkflowStates[0].Id},
		"unknown assignee":    {"assigneeId": newID()},
	} {
		if got := ada.do("PATCH", issuePath, body, nil); got != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, got)
		}
	}
	ada.expect(http.StatusBadRequest, "POST", issuesPath, api.CreateIssueRequest{Id: newID(), Title: ""}, nil)
	ada.expect(http.StatusConflict, "POST", "/api/teams/"+foreign.Teams[0].Id.String()+"/issues", req, nil)

	// Archiving removes the issue from later bootstraps; repeating it is a no-op.
	var archived, again api.IssueMutationResult
	ada.expect(http.StatusOK, "DELETE", "/api/issues/"+second.Issue.Id.String(), nil, &archived)
	logged = ada.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "DELETE", "/api/issues/"+second.Issue.Id.String(), nil, &again)
	if archived.Issue.ArchivedAt == nil || again.SyncId != logged {
		t.Errorf("archive = %+v, repeated = %+v", archived, again)
	}
	after := ada.bootstrap(ws.Id)
	if len(after.Issues) != 1 || after.Issues[0].Title != "Fix sign-in" || after.LastSyncId != logged {
		t.Errorf("bootstrap after archive: %d issues, lastSyncId %d (want 1, %d)", len(after.Issues), after.LastSyncId, logged)
	}

	// The log replays exactly those issue changes, in order (each alongside
	// its activity entries), and no sync ID was skipped.
	batch := env.fetch(t, ws.Id, adaMe.User, api.WorkspaceRoleAdmin, boot.LastSyncId)
	want := []string{"I:Issue", "I:Issue", "U:Issue", "U:Issue", "U:Issue", "U:Issue"}
	if got := only(batch, api.SyncModelIssue); len(got) != len(want) {
		t.Fatalf("issue log = %v, want %v", got, want)
	}
	var last api.Issue
	for i, a := range batch.Actions {
		if a.SyncID != boot.LastSyncId+int64(i)+1 {
			t.Errorf("action %d has sync ID %d, want %d", i, a.SyncID, boot.LastSyncId+int64(i)+1)
		}
		if a.Model == string(api.SyncModelIssue) {
			if err := json.Unmarshal(a.Data, &last); err != nil {
				t.Fatal(err)
			}
		}
	}
	if last.ArchivedAt == nil {
		t.Errorf("the last issue action is %+v, want the archived issue", last)
	}
	if batch.Cursor != after.LastSyncId || batch.More || batch.Reset {
		t.Errorf("batch cursor %d more %v reset %v", batch.Cursor, batch.More, batch.Reset)
	}

	// Outsiders can't touch issues or bootstrap the workspace.
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", "/api/workspaces/"+ws.Id.String()+"/sync/bootstrap", nil, nil)
	eve.expect(http.StatusNotFound, "POST", issuesPath, api.CreateIssueRequest{Id: newID(), Title: "Nope"}, nil)
	eve.expect(http.StatusNotFound, "PATCH", issuePath, map[string]any{"title": "Nope"}, nil)
	eve.expect(http.StatusNotFound, "DELETE", issuePath, nil, nil)
	eve.expect(http.StatusNotFound, "GET", "/api/workspaces/"+ws.Id.String()+"/sync/ws?after=0", nil, nil)
	env.client(t).expect(http.StatusUnauthorized, "GET", "/api/workspaces/"+ws.Id.String()+"/sync/ws?after=0", nil, nil)
}

func TestSyncVisibility(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	base := "/api/workspaces/" + ws.Id.String()
	public := ada.bootstrap(ws.Id).Teams[0]

	member, guest := api.WorkspaceRoleMember, api.WorkspaceRoleGuest
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "bob@example.com", Role: &member}, nil)
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "gus@example.com", Role: &guest}, nil)
	bob, bobMe := env.signIn(t, "bob@example.com")
	gus, gusMe := env.signIn(t, "gus@example.com")

	private := true
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)

	bobBoot, gusBoot := bob.bootstrap(ws.Id), gus.bootstrap(ws.Id)
	if len(bobBoot.Teams) != 1 || len(bobBoot.WorkflowStates) != 5 {
		t.Errorf("member bootstrap: %d teams, %d states; want the public team only", len(bobBoot.Teams), len(bobBoot.WorkflowStates))
	}
	if len(gusBoot.Teams) != 0 || len(gusBoot.WorkflowStates) != 0 || len(gusBoot.Users) != 3 {
		t.Errorf("guest bootstrap: %d teams, %d states, %d users; want 0, 0, 3", len(gusBoot.Teams), len(gusBoot.WorkflowStates), len(gusBoot.Users))
	}

	ada.createIssue(secret.Id, "Secret plan")
	ada.createIssue(public.Id, "Public plan")

	// A member sees the public team's changes but not the private team's.
	if got := models(env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, bobBoot.LastSyncId)); len(got) != 2 || got[0] != "I:Issue" || got[1] != "I:Activity" {
		t.Errorf("member's log = %v, want only the public issue and its activity", got)
	}
	// A guest in no teams sees neither, though the cursor still advances.
	gusBatch := env.fetch(t, ws.Id, gusMe.User, api.WorkspaceRoleGuest, gusBoot.LastSyncId)
	if len(gusBatch.Actions) != 0 || gusBatch.Cursor != ada.lastSyncID(ws.Id) {
		t.Errorf("guest's log = %v with cursor %d", models(gusBatch), gusBatch.Cursor)
	}

	// Joining a public team is an ordinary change for a member...
	bobCursor := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, bobBoot.LastSyncId).Cursor
	bob.expect(http.StatusNoContent, "PUT", "/api/teams/"+public.Id.String()+"/members/"+bobMe.User.Id.String(), nil, nil)
	joined := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, bobCursor)
	if got := models(joined); joined.Reset || len(got) != 1 || got[0] != "I:TeamMember" {
		t.Errorf("after joining a public team: reset %v, log %v", joined.Reset, got)
	}
	// ...and adding the same member again logs nothing.
	bob.expect(http.StatusNoContent, "PUT", "/api/teams/"+public.Id.String()+"/members/"+bobMe.User.Id.String(), nil, nil)
	if again := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, joined.Cursor); len(again.Actions) != 0 {
		t.Errorf("re-adding a member logged %v", models(again))
	}

	// ...but gaining access to a private team, or any team as a guest, means
	// the client is missing history and must bootstrap again.
	ada.expect(http.StatusNoContent, "PUT", "/api/teams/"+secret.Id.String()+"/members/"+bobMe.User.Id.String(), nil, nil)
	if batch := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, joined.Cursor); !batch.Reset {
		t.Error("joining a private team did not ask the client to reset")
	}
	ada.expect(http.StatusNoContent, "PUT", "/api/teams/"+public.Id.String()+"/members/"+gusMe.User.Id.String(), nil, nil)
	if batch := env.fetch(t, ws.Id, gusMe.User, api.WorkspaceRoleGuest, gusBatch.Cursor); !batch.Reset {
		t.Error("a guest joining a team did not ask the client to reset")
	}
	if boot := bob.bootstrap(ws.Id); len(boot.Teams) != 2 || len(boot.Issues) != 2 {
		t.Errorf("member bootstrap after joining: %d teams, %d issues; want 2, 2", len(boot.Teams), len(boot.Issues))
	}
	if boot := gus.bootstrap(ws.Id); len(boot.Teams) != 1 || len(boot.Issues) != 1 {
		t.Errorf("guest bootstrap after joining: %d teams, %d issues; want 1, 1", len(boot.Teams), len(boot.Issues))
	}

	// Renaming a user reaches everyone in their workspaces.
	before := ada.bootstrap(ws.Id).LastSyncId
	bob.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"name": "Bob"}, nil)
	renamed := env.fetch(t, ws.Id, gusMe.User, api.WorkspaceRoleGuest, before)
	if got := models(renamed); len(got) != 1 || got[0] != "U:User" {
		t.Errorf("after rename, guest's log = %v", got)
	}
}

func TestSyncCursorValidity(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	last := ada.bootstrap(ws.Id).LastSyncId
	ctx := context.Background()

	check := func(after int64, want bool, why string) {
		t.Helper()
		got, err := syncer.CursorValid(ctx, env.q, ws.Id, after)
		if err != nil || got != want {
			t.Errorf("CursorValid(%d) = %v, %v; want %v (%s)", after, got, err, want, why)
		}
	}
	check(last, true, "up to date")
	check(0, true, "the whole log is still there")
	check(last+1, false, "ahead of the workspace")
	check(-1, false, "negative")

	// Once the oldest entries are pruned, clients that far behind must reset.
	if _, err := env.pool.Exec(ctx, `DELETE FROM sync_actions WHERE workspace_id = $1 AND sync_id <= 3`, ws.Id); err != nil {
		t.Fatal(err)
	}
	check(2, false, "pruned")
	check(3, true, "first retained entry is next")
}

func TestConcurrentIssueCreation(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	boot := ada.bootstrap(ws.Id)
	path := "/api/teams/" + boot.Teams[0].Id.String() + "/issues"

	const n = 20
	results := make([]api.IssueMutationResult, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A client per goroutine: the test client isn't safe for concurrent use.
			c := &client{t: t, env: env, cookies: ada.cookies}
			req := api.CreateIssueRequest{Id: newID(), Title: "Concurrent"}
			if got := c.do("POST", path, req, &results[i]); got != http.StatusCreated {
				t.Errorf("create %d: status %d", i, got)
			}
		}()
	}
	wg.Wait()

	// Every issue got its own number and its own sync ID.
	numbers, syncIDs := map[int32]bool{}, map[int64]bool{}
	for _, r := range results {
		numbers[r.Issue.Number] = true
		syncIDs[r.SyncId] = true
	}
	for i := 1; i <= n; i++ {
		if !numbers[int32(i)] {
			t.Errorf("no issue numbered %d", i)
		}
	}
	if len(syncIDs) != n {
		t.Errorf("%d distinct sync IDs for %d issues", len(syncIDs), n)
	}
	// The log has no gaps: an issue and its activity entry per create.
	rows, err := env.q.ListSyncActionsAfter(context.Background(), dbgen.ListSyncActionsAfterParams{WorkspaceID: ws.Id, SyncID: boot.LastSyncId, Limit: 100})
	if err != nil || len(rows) != 2*n {
		t.Fatalf("log has %d entries (err %v), want %d", len(rows), err, 2*n)
	}
	for i, row := range rows {
		if row.SyncID != boot.LastSyncId+int64(i)+1 {
			t.Fatalf("log entry %d has sync ID %d, want %d", i, row.SyncID, boot.LastSyncId+int64(i)+1)
		}
	}
}
