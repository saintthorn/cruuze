package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

// stateOfType returns the team's first state of a type from a bootstrap.
func stateOfType(t *testing.T, boot api.SyncBootstrap, teamID uuid.UUID, kind api.WorkflowStateType) api.WorkflowState {
	t.Helper()
	for _, s := range boot.WorkflowStates {
		if s.TeamId == teamID && s.Type == kind {
			return s
		}
	}
	t.Fatalf("team has no %s state", kind)
	return api.WorkflowState{}
}

func TestWorkflowStates(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	statesPath := "/api/teams/" + team.Id.String() + "/states"
	boot := ada.bootstrap(ws.Id)

	// Bob can see the team but isn't in it: he doesn't get to reshape it.
	review := map[string]any{"id": newID(), "name": " In Review ", "type": "started", "color": "#bb87fc"}
	bob.expect(http.StatusForbidden, "POST", statesPath, review, nil)

	ada.expect(http.StatusBadRequest, "POST", statesPath, map[string]any{"id": newID(), "name": "Inbox", "type": "triage", "color": "#bb87fc"}, nil)
	ada.expect(http.StatusBadRequest, "POST", statesPath, map[string]any{"id": newID(), "name": "", "type": "started", "color": "#bb87fc"}, nil)
	ada.expect(http.StatusBadRequest, "POST", statesPath, map[string]any{"id": newID(), "name": "X", "type": "started", "color": "purple"}, nil)
	ada.expect(http.StatusConflict, "POST", statesPath, map[string]any{"id": newID(), "name": "in progress", "type": "started", "color": "#bb87fc"}, nil)

	// Create, with an idempotent retry. It goes last by default.
	before := ada.lastSyncID(ws.Id)
	ada.expect(http.StatusCreated, "POST", statesPath, review, nil)
	ada.expect(http.StatusOK, "POST", statesPath, review, nil)
	if after := ada.lastSyncID(ws.Id); after != before+1 {
		t.Errorf("creating a state wrote %d sync actions, want 1", after-before)
	}
	var created api.WorkflowState
	states := ada.bootstrap(ws.Id).WorkflowStates
	for _, s := range states {
		if s.Name == "In Review" {
			created = s
		}
	}
	if created.Type != api.WorkflowStateTypeStarted || created.Position != states[len(states)-2].Position+1 || len(states) != 6 {
		t.Fatalf("created state = %+v among %d", created, len(states))
	}
	statePath := "/api/states/" + created.Id.String()

	// Rename, recolor and move it between In Progress and Done.
	ada.expect(http.StatusConflict, "PATCH", statePath, map[string]any{"name": "Done"}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", statePath, map[string]any{"color": "nope"}, nil)
	bob.expect(http.StatusForbidden, "PATCH", statePath, map[string]any{"name": "Mine"}, nil)
	ada.expect(http.StatusOK, "PATCH", statePath, map[string]any{"name": "Review", "color": "#26b5ce", "position": 2.5}, nil)
	ordered := ada.bootstrap(ws.Id).WorkflowStates
	if ordered[3].Name != "Review" || ordered[3].Color != "#26b5ce" {
		t.Errorf("states after the move = %+v", ordered)
	}

	// Issues can be put in it, and then it can't be deleted.
	issue := ada.createIssue(team.Id, "Needs eyes").Issue
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"stateId": created.Id}, nil)
	ada.expect(http.StatusConflict, "DELETE", statePath, nil, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"stateId": stateOfType(t, boot, team.Id, api.WorkflowStateTypeBacklog).Id}, nil)
	bob.expect(http.StatusForbidden, "DELETE", statePath, nil, nil)
	ada.expect(http.StatusOK, "DELETE", statePath, nil, nil)
	ada.expect(http.StatusNotFound, "DELETE", statePath, nil, nil)

	// The team keeps somewhere for new issues and for finished ones.
	ada.expect(http.StatusConflict, "DELETE", "/api/states/"+stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted).Id.String(), nil, nil)
	ada.expect(http.StatusOK, "DELETE", "/api/states/"+stateOfType(t, boot, team.Id, api.WorkflowStateTypeUnstarted).Id.String(), nil, nil)
	// The backlog is now the last open state, and has an issue in it anyway.
	ada.expect(http.StatusConflict, "DELETE", "/api/states/"+stateOfType(t, boot, team.Id, api.WorkflowStateTypeBacklog).Id.String(), nil, nil)
}

func TestIssueLifecycleTimestamps(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	boot := ada.bootstrap(ws.Id)
	team := boot.Teams[0]
	started := stateOfType(t, boot, team.Id, api.WorkflowStateTypeStarted)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)
	canceled := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCanceled)
	backlog := stateOfType(t, boot, team.Id, api.WorkflowStateTypeBacklog)

	issue := ada.createIssue(team.Id, "Track me").Issue
	if issue.StartedAt != nil || issue.CompletedAt != nil {
		t.Fatalf("new issue = %+v, want no lifecycle timestamps", issue)
	}
	path := "/api/issues/" + issue.Id.String()
	move := func(state api.WorkflowState) api.Issue {
		t.Helper()
		var out api.IssueMutationResult
		ada.expect(http.StatusOK, "PATCH", path, map[string]any{"stateId": state.Id}, &out)
		return out.Issue
	}

	inProgress := move(started)
	if inProgress.StartedAt == nil || inProgress.CompletedAt != nil {
		t.Errorf("after starting: %+v", inProgress)
	}
	finished := move(done)
	if finished.CompletedAt == nil || !finished.StartedAt.Equal(*inProgress.StartedAt) {
		t.Errorf("after completing: %+v", finished)
	}
	// Canceled counts as closed; reopening clears the end but keeps the start.
	if got := move(canceled); got.CompletedAt == nil {
		t.Errorf("after canceling: %+v", got)
	}
	reopened := move(backlog)
	if reopened.CompletedAt != nil || reopened.StartedAt == nil {
		t.Errorf("after reopening: %+v", reopened)
	}
	// A change that isn't a state change leaves them alone.
	var renamed api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", path, map[string]any{"title": "Still tracked"}, &renamed)
	if renamed.Issue.CompletedAt != nil || !renamed.Issue.StartedAt.Equal(*reopened.StartedAt) {
		t.Errorf("after renaming: %+v", renamed.Issue)
	}
	// The sync log carries them too.
	for _, i := range ada.bootstrap(ws.Id).Issues {
		if i.Id == issue.Id && i.StartedAt == nil {
			t.Error("bootstrap issue has no startedAt")
		}
	}

	// One history row per stay: backlog, started, completed, canceled, backlog.
	rows, err := env.pool.Query(context.Background(),
		`SELECT state_type, left_at IS NULL FROM issue_state_history WHERE issue_id = $1 ORDER BY id`, issue.Id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var kind string
		var current bool
		if err := rows.Scan(&kind, &current); err != nil {
			t.Fatal(err)
		}
		if current {
			kind += "*"
		}
		got = append(got, kind)
	}
	want := []string{"backlog", "started", "completed", "canceled", "backlog*"}
	if len(got) != len(want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("history = %v, want %v", got, want)
		}
	}
}

func TestBatchIssueUpdates(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, team := workspaceWithMember(t, env)
	boot := ada.bootstrap(ws.Id)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)

	a := ada.createIssue(team.Id, "A").Issue
	b := ada.createIssue(team.Id, "B").Issue
	c := ada.createIssue(team.Id, "C").Issue
	update := func(id uuid.UUID, patch map[string]any) map[string]any {
		return map[string]any{"issueId": id, "patch": patch}
	}
	titles := func() map[uuid.UUID]api.Issue {
		out := map[uuid.UUID]api.Issue{}
		for _, i := range ada.bootstrap(ws.Id).Issues {
			out[i.Id] = i
		}
		return out
	}

	ada.expect(http.StatusBadRequest, "POST", "/api/issues/batch", map[string]any{"updates": []any{}}, nil)

	// Three issues change in one request; the last sync ID covers all of it.
	var ack api.SyncAck
	ada.expect(http.StatusOK, "POST", "/api/issues/batch", map[string]any{"updates": []any{
		update(a.Id, map[string]any{"stateId": done.Id}),
		update(b.Id, map[string]any{"stateId": done.Id, "assigneeId": bobMe.User.Id}),
		update(c.Id, map[string]any{"priority": 1}),
		update(c.Id, map[string]any{"priority": 1}), // already so: not an error
	}}, &ack)
	after := titles()
	if after[a.Id].StateId != done.Id || after[b.Id].StateId != done.Id || after[b.Id].AssigneeId == nil || after[c.Id].Priority != 1 {
		t.Errorf("after the batch: %+v", after)
	}
	if last := ada.lastSyncID(ws.Id); ack.SyncId > last || ack.SyncId == 0 {
		t.Errorf("ack sync ID %d, log is at %d", ack.SyncId, last)
	}
	if got := bob.notificationTypes(ws.Id); len(got) != 1 || got[0] != api.NotificationTypeAssigned {
		t.Errorf("bob's notifications = %v, want [assigned]", got)
	}

	// One bad update and nothing in the batch applies.
	ada.expect(http.StatusBadRequest, "POST", "/api/issues/batch", map[string]any{"updates": []any{
		update(a.Id, map[string]any{"title": "Changed"}),
		update(b.Id, map[string]any{"priority": 9}),
	}}, nil)
	ada.expect(http.StatusBadRequest, "POST", "/api/issues/batch", map[string]any{"updates": []any{
		update(a.Id, map[string]any{"title": "Changed"}),
		update(b.Id, map[string]any{"stateId": newID()}),
	}}, nil)
	if got := titles()[a.Id].Title; got != "A" {
		t.Errorf("a failed batch still renamed an issue to %q", got)
	}

	// A batch can't reach issues its sender can't see.
	private := true
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)
	hidden := ada.createIssue(secret.Id, "Hidden").Issue
	bob.expect(http.StatusNotFound, "POST", "/api/issues/batch", map[string]any{"updates": []any{
		update(c.Id, map[string]any{"title": "Mine now"}),
		update(hidden.Id, map[string]any{"title": "Mine too"}),
	}}, nil)
	if got := titles()[c.Id].Title; got != "C" {
		t.Errorf("a refused batch still renamed an issue to %q", got)
	}
}

func TestArchivedIssuesStayReachable(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	boot := ada.bootstrap(ws.Id)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)

	var created api.IssueMutationResult
	description := "The quarterly export times out."
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/issues",
		api.CreateIssueRequest{Id: newID(), Title: "Old export bug", Description: &description}, &created)
	old := created.Issue
	fresh := ada.createIssue(team.Id, "Finished yesterday").Issue
	open := ada.createIssue(team.Id, "Still open").Issue
	for _, id := range []uuid.UUID{old.Id, fresh.Id} {
		ada.expect(http.StatusOK, "PATCH", "/api/issues/"+id.String(), map[string]any{"stateId": done.Id}, nil)
	}
	ctx := context.Background()
	// Pretend the first was closed long ago, and the open one created long ago.
	if _, err := env.pool.Exec(ctx, `UPDATE issues SET completed_at = now() - interval '100 days' WHERE id = $1`, old.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE issues SET created_at = now() - interval '400 days' WHERE id = $1`, open.Id); err != nil {
		t.Fatal(err)
	}

	// Only what has been closed for longer than the team's window goes.
	n, err := env.srv.archiveClosedIssues(ctx, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("archived %d issues (%v), want 1", n, err)
	}
	if n, _ := env.srv.archiveClosedIssues(ctx, time.Now()); n != 0 {
		t.Errorf("a second pass archived %d more", n)
	}
	remaining := map[uuid.UUID]bool{}
	for _, i := range ada.bootstrap(ws.Id).Issues {
		remaining[i.Id] = true
	}
	if remaining[old.Id] || !remaining[fresh.Id] || !remaining[open.Id] {
		t.Errorf("after auto-archive the bootstrap has %v", remaining)
	}

	// It can still be looked up, by number or ID, by those who may see it.
	var found api.Issue
	bob.expect(http.StatusOK, "GET", base+"/issues/lookup?ref=acm-1", nil, &found)
	if found.Id != old.Id || found.ArchivedAt == nil {
		t.Errorf("lookup by number = %+v", found)
	}
	bob.expect(http.StatusOK, "GET", base+"/issues/lookup?ref="+old.Id.String(), nil, &found)
	bob.expect(http.StatusNotFound, "GET", base+"/issues/lookup?ref=ACM-99", nil, nil)
	bob.expect(http.StatusNotFound, "GET", base+"/issues/lookup?ref=nonsense", nil, nil)
	if details := bob.details("/api/issues/" + old.Id.String()); len(details.Activities) != 3 {
		t.Errorf("archived issue has %d activities, want created, state, archived", len(details.Activities))
	}
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", base+"/issues/lookup?ref=ACM-1", nil, nil)

	// And found by search, marked as archived, after what is still live.
	var results api.SearchResults
	bob.expect(http.StatusOK, "GET", base+"/search?q=export", nil, &results)
	if len(results.Results) != 1 || !results.Results[0].Archived || results.Results[0].Identifier != "ACM-1" || results.Results[0].Title != "Old export bug" {
		t.Errorf("search results = %+v", results.Results)
	}

	// Unarchiving brings it back for everyone.
	cursor := bob.lastSyncID(ws.Id)
	var back api.IssueMutationResult
	bob.expect(http.StatusOK, "POST", "/api/issues/"+old.Id.String()+"/unarchive", nil, &back)
	if back.Issue.ArchivedAt != nil {
		t.Errorf("unarchived issue = %+v", back.Issue)
	}
	if got := only(env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, cursor), api.SyncModelIssue); len(got) != 1 || got[0] != "U:Issue" {
		t.Errorf("sync log after unarchive = %v", got)
	}
	before := ada.lastSyncID(ws.Id)
	bob.expect(http.StatusOK, "POST", "/api/issues/"+old.Id.String()+"/unarchive", nil, nil)
	if after := ada.lastSyncID(ws.Id); after != before {
		t.Errorf("unarchiving twice wrote %d sync actions", after-before)
	}

	// A team can turn auto-archive off.
	var result api.TeamMutationResult
	ada.expect(http.StatusBadRequest, "PATCH", "/api/teams/"+team.Id.String(), map[string]any{"autoArchiveDays": -1}, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/teams/"+team.Id.String(), map[string]any{"autoArchiveDays": 0}, &result)
	if result.Team.AutoArchiveDays != 0 {
		t.Errorf("team = %+v", result.Team)
	}
	if n, _ := env.srv.archiveClosedIssues(ctx, time.Now()); n != 0 {
		t.Errorf("archived %d issues with auto-archive off", n)
	}
}

func TestHygiene(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, _, ws, team := workspaceWithMember(t, env)
	ctx := context.Background()
	if team.StaleDays != 60 {
		t.Errorf("new team's staleDays = %d, want 60", team.StaleDays)
	}

	// Touching an issue freshens it without changing anything about it.
	issue := ada.createIssue(team.Id, "Still wanted").Issue
	if _, err := env.pool.Exec(ctx, `UPDATE issues SET updated_at = now() - interval '90 days' WHERE id = $1`, issue.Id); err != nil {
		t.Fatal(err)
	}
	path := "/api/issues/" + issue.Id.String()
	var touched api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", path, map[string]any{"touch": true}, &touched)
	if time.Since(touched.Issue.UpdatedAt) > time.Minute || touched.Issue.Title != issue.Title {
		t.Errorf("touched issue = %+v", touched.Issue)
	}
	activities := ada.details(path).Activities
	if last := activities[len(activities)-1]; last.Type != api.ActivityTypeReviewed || last.ActorId == nil || *last.ActorId != adaMe.User.Id {
		t.Errorf("last activity = %+v, want reviewed by ada", last)
	}
	// touch: false is nothing at all.
	before := ada.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", path, map[string]any{"touch": false}, nil)
	if after := ada.lastSyncID(ws.Id); after != before {
		t.Errorf("touch: false wrote %d sync actions", after-before)
	}

	// The archive is listed newest first, a page at a time.
	var ids []uuid.UUID
	for i := range 3 {
		archived := ada.createIssue(team.Id, "Old "+string(rune('A'+i))).Issue
		ada.expect(http.StatusOK, "DELETE", "/api/issues/"+archived.Id.String(), nil, nil)
		if _, err := env.pool.Exec(ctx, `UPDATE issues SET archived_at = now() - make_interval(days => $2) WHERE id = $1`, archived.Id, 3-i); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, archived.Id)
	}
	var page api.ArchivedIssues
	archivedPath := "/api/teams/" + team.Id.String() + "/archived"
	bob.expect(http.StatusOK, "GET", archivedPath, nil, &page)
	if len(page.Issues) != 3 || page.More || page.Issues[0].Id != ids[2] || page.Issues[2].Id != ids[0] {
		t.Errorf("archived page = %d issues, more %v", len(page.Issues), page.More)
	}
	bob.expect(http.StatusOK, "GET", archivedPath+"?before="+page.Issues[0].ArchivedAt.UTC().Format(time.RFC3339Nano), nil, &page)
	if len(page.Issues) != 2 || page.Issues[0].Id != ids[1] {
		t.Errorf("second page = %+v", page.Issues)
	}
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", archivedPath, nil, nil)

	var result api.TeamMutationResult
	ada.expect(http.StatusBadRequest, "PATCH", "/api/teams/"+team.Id.String(), map[string]any{"staleDays": -5}, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/teams/"+team.Id.String(), map[string]any{"staleDays": 30}, &result)
	if result.Team.StaleDays != 30 {
		t.Errorf("team = %+v", result.Team)
	}
}

func TestAutoCloseUntouched(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	ctx := context.Background()
	if team.AutoCloseDays != 0 {
		t.Errorf("new team's autoCloseDays = %d, want 0", team.AutoCloseDays)
	}
	boot := ada.bootstrap(ws.Id)
	started := stateOfType(t, boot, team.Id, api.WorkflowStateTypeStarted)
	canceled := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCanceled)
	age := func(id uuid.UUID, days int) {
		t.Helper()
		if _, err := env.pool.Exec(ctx, `UPDATE issues SET updated_at = now() - make_interval(days => $2) WHERE id = $1`, id, days); err != nil {
			t.Fatal(err)
		}
	}

	forgotten := bob.createIssue(team.Id, "Forgotten").Issue
	recent := ada.createIssue(team.Id, "Recent").Issue
	underway := ada.createIssue(team.Id, "Underway").Issue
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+underway.Id.String(), map[string]any{"stateId": started.Id}, nil)
	age(forgotten.Id, 100)
	age(underway.Id, 100)
	age(recent.Id, 10)

	// Off by default: nothing happens.
	if n, err := env.srv.closeUntouchedIssues(ctx, time.Now()); err != nil || n != 0 {
		t.Fatalf("with auto-close off: closed %d, %v", n, err)
	}
	teamPath := "/api/teams/" + team.Id.String()
	ada.expect(http.StatusBadRequest, "PATCH", teamPath, map[string]any{"autoCloseDays": 3}, nil)
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"autoCloseDays": 90}, nil)

	// Only what was never started and has sat past the window is canceled.
	if n, err := env.srv.closeUntouchedIssues(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("closed %d, %v; want 1", n, err)
	}
	if got := ada.issue(ws.Id, forgotten.Id); got.StateId != canceled.Id || got.CompletedAt == nil {
		t.Errorf("forgotten issue = %+v, want canceled", got)
	}
	if got := ada.issue(ws.Id, underway.Id).StateId; got != started.Id {
		t.Error("work in progress was canceled")
	}
	if got := ada.issue(ws.Id, recent.Id).StateId; got == canceled.Id {
		t.Error("a recent issue was canceled")
	}
	// It is recorded as the system's doing, and its creator is told.
	activities := ada.details("/api/issues/" + forgotten.Id.String()).Activities
	if last := activities[len(activities)-1]; last.Type != api.ActivityTypeState || last.ActorId != nil {
		t.Errorf("last activity = %+v, want a status change by nobody", last)
	}
	if got := bob.notificationTypes(ws.Id); !sameTypes(got, api.NotificationTypeStatus) {
		t.Errorf("bob's notifications = %v, want a status change", got)
	}
	// A second pass finds nothing more.
	if n, _ := env.srv.closeUntouchedIssues(ctx, time.Now()); n != 0 {
		t.Errorf("second pass closed %d", n)
	}
}
