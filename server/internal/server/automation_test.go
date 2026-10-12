package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

// githubSend posts a signed GitHub event of any kind.
func (e *testEnv) githubSend(workspaceID uuid.UUID, secret, kind string, payload map[string]any) int {
	body, _ := json.Marshal(payload)
	return e.send("POST", "/api/integrations/github/"+workspaceID.String(), body,
		"Content-Type", "application/json",
		"X-GitHub-Event", kind,
		"X-Hub-Signature-256", "sha256="+signWebhook(secret, body),
	).StatusCode
}

func (c *client) issue(workspaceID, issueID uuid.UUID) api.Issue {
	c.t.Helper()
	for _, i := range c.bootstrap(workspaceID).Issues {
		if i.Id == issueID {
			return i
		}
	}
	c.t.Fatalf("issue %s is not in the bootstrap", issueID)
	return api.Issue{}
}

func TestTeamAutomation(t *testing.T) {
	env := newTestEnv(t)
	ada, _, adaMe, bobMe, ws, team := workspaceWithMember(t, env)
	boot := ada.bootstrap(ws.Id)
	started := stateOfType(t, boot, team.Id, api.WorkflowStateTypeStarted)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)
	canceled := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCanceled)
	teamPath := "/api/teams/" + team.Id.String()
	move := func(issueID uuid.UUID, state api.WorkflowState, extra map[string]any) api.Issue {
		t.Helper()
		patch := map[string]any{"stateId": state.Id}
		for k, v := range extra {
			patch[k] = v
		}
		var out api.IssueMutationResult
		ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issueID.String(), patch, &out)
		return out.Issue
	}

	if !team.AutoAssign || !team.AutoCycle || !team.AutoCloseParent || team.ReviewStateId != nil {
		t.Fatalf("a new team's automation = %+v, want everything on and no review status", team)
	}

	// Starting an unassigned issue makes it yours, in the same change.
	before := ada.lastSyncID(ws.Id)
	mine := move(ada.createIssue(team.Id, "Unowned").Issue.Id, started, nil)
	if mine.AssigneeId == nil || *mine.AssigneeId != adaMe.User.Id {
		t.Errorf("after starting: assignee = %v, want ada", mine.AssigneeId)
	}
	// created (issue + activity), then one update with its two activities.
	if after := ada.lastSyncID(ws.Id); after != before+5 {
		t.Errorf("create and start wrote %d sync actions, want 5", after-before)
	}
	// Unless the same change says who.
	theirs := move(ada.createIssue(team.Id, "For bob").Issue.Id, started, map[string]any{"assigneeId": bobMe.User.Id})
	if theirs.AssigneeId == nil || *theirs.AssigneeId != bobMe.User.Id {
		t.Errorf("explicit assignee was overridden: %v", theirs.AssigneeId)
	}
	nobody := move(ada.createIssue(team.Id, "Deliberately unowned").Issue.Id, started, map[string]any{"assigneeId": nil})
	if nobody.AssigneeId != nil {
		t.Errorf("explicitly unassigned issue got an assignee: %v", nobody.AssigneeId)
	}

	// With cycles on, started issues join the current one.
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"cycleWeeks": 2}, nil)
	cycles := ada.bootstrap(ws.Id).Cycles
	if len(cycles) == 0 {
		t.Fatal("no cycles after turning them on")
	}
	inCycle := move(ada.createIssue(team.Id, "This sprint").Issue.Id, started, nil)
	if inCycle.CycleId == nil || *inCycle.CycleId != cycles[0].Id {
		t.Errorf("started issue's cycle = %v, want the current one %v", inCycle.CycleId, cycles[0].Id)
	}

	// A parent completes when its last open sub-issue is closed.
	parent := ada.createIssue(team.Id, "Epic").Issue
	child := func(title string) api.Issue {
		var out api.IssueMutationResult
		ada.expect(http.StatusCreated, "POST", teamPath+"/issues", map[string]any{"id": newID(), "title": title, "parentId": parent.Id}, &out)
		return out.Issue
	}
	one, two := child("Part one"), child("Part two")
	move(one.Id, done, nil)
	if got := ada.issue(ws.Id, parent.Id); got.CompletedAt != nil {
		t.Errorf("parent closed with a sub-issue still open")
	}
	move(two.Id, canceled, nil)
	closed := ada.issue(ws.Id, parent.Id)
	if closed.StateId != done.Id || closed.CompletedAt == nil {
		t.Errorf("parent after its sub-issues closed = %+v, want completed", closed)
	}
	// The system did it: the activity has no actor.
	activities := ada.details("/api/issues/" + parent.Id.String()).Activities
	if last := activities[len(activities)-1]; last.Type != api.ActivityTypeState || last.ActorId != nil {
		t.Errorf("parent's last activity = %+v, want a state change with no actor", last)
	}

	// Each of these can be turned off.
	var result api.TeamMutationResult
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"autoAssign": false, "autoCycle": false, "autoCloseParent": false}, &result)
	if result.Team.AutoAssign || result.Team.AutoCycle || result.Team.AutoCloseParent {
		t.Fatalf("team = %+v, want automation off", result.Team)
	}
	plain := move(ada.createIssue(team.Id, "Hands off").Issue.Id, started, nil)
	if plain.AssigneeId != nil || plain.CycleId != nil {
		t.Errorf("with automation off: %+v", plain)
	}
	parent2 := ada.createIssue(team.Id, "Epic two").Issue
	var only api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", teamPath+"/issues", map[string]any{"id": newID(), "title": "Only part", "parentId": parent2.Id}, &only)
	move(only.Issue.Id, done, nil)
	if got := ada.issue(ws.Id, parent2.Id); got.CompletedAt != nil {
		t.Errorf("parent closed although the team turned that off")
	}
}

func TestGithubDrivesStatus(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, _, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	teamPath := "/api/teams/" + team.Id.String()
	boot := ada.bootstrap(ws.Id)
	backlog := stateOfType(t, boot, team.Id, api.WorkflowStateTypeBacklog)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)

	var integration api.GithubIntegration
	ada.expect(http.StatusOK, "PUT", base+"/integrations/github", nil, &integration)
	secret := *integration.Secret

	// Ada says who she is on GitHub, so her pushes are hers here too.
	ada.expect(http.StatusBadRequest, "PATCH", "/api/me", map[string]any{"githubLogin": "not a login!"}, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"githubLogin": "ada-gh"}, nil)
	var me api.Me
	ada.expect(http.StatusOK, "GET", "/api/me", nil, &me)
	if me.GithubLogin != "ada-gh" {
		t.Errorf("githubLogin = %q", me.GithubLogin)
	}

	// A review status: one of the team's started states, nobody else's.
	review := map[string]any{"id": newID(), "name": "In Review", "type": "started", "color": "#bb87fc"}
	ada.expect(http.StatusCreated, "POST", teamPath+"/states", review, nil)
	ada.expect(http.StatusBadRequest, "PATCH", teamPath, map[string]any{"reviewStateId": done.Id}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", teamPath, map[string]any{"reviewStateId": newID()}, nil)
	var result api.TeamMutationResult
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"reviewStateId": review["id"]}, &result)
	if result.Team.ReviewStateId == nil {
		t.Fatalf("team = %+v, want a review status", result.Team)
	}
	reviewID := *result.Team.ReviewStateId

	one := ada.createIssue(team.Id, "Fix login").Issue    // ACM-1
	two := ada.createIssue(team.Id, "Fix logout").Issue   // ACM-2
	three := ada.createIssue(team.Id, "Fix signup").Issue // ACM-3
	repo := map[string]any{"full_name": "acme/app", "default_branch": "main"}

	// Creating a branch named after an issue starts it, as the person who did.
	branch := map[string]any{"ref": "ada/acm-1-fix-login", "ref_type": "branch", "repository": repo, "sender": map[string]any{"login": "Ada-GH"}}
	if got := env.githubSend(ws.Id, secret, "create", branch); got != http.StatusNoContent {
		t.Fatalf("create event: status %d", got)
	}
	startedIssue := ada.issue(ws.Id, one.Id)
	if startedIssue.StartedAt == nil || startedIssue.AssigneeId == nil || *startedIssue.AssigneeId != adaMe.User.Id {
		t.Errorf("after the branch was created: %+v, want started and assigned to ada", startedIssue)
	}
	activities := ada.details("/api/issues/" + one.Id.String()).Activities
	if last := activities[len(activities)-1]; last.ActorId == nil || *last.ActorId != adaMe.User.Id {
		t.Errorf("the start is credited to %v, want ada", last.ActorId)
	}
	// A tag is not a branch.
	env.githubSend(ws.Id, secret, "create", map[string]any{"ref": "acm-2", "ref_type": "tag", "repository": repo})
	if got := ada.issue(ws.Id, two.Id); got.StartedAt != nil {
		t.Errorf("a tag started an issue")
	}

	// Asking for review moves it to the team's review status.
	pr := map[string]any{
		"html_url": "https://github.com/acme/app/pull/7", "number": 7, "title": "Fix login", "state": "open",
		"head": map[string]any{"ref": "ada/acm-1-fix-login"}, "user": map[string]any{"login": "ada-gh"},
	}
	event := func(action string) map[string]any {
		return map[string]any{"action": action, "pull_request": pr, "repository": repo, "sender": map[string]any{"login": "ada-gh"}}
	}
	env.githubSend(ws.Id, secret, "pull_request", event("opened"))
	if got := ada.issue(ws.Id, one.Id); got.StateId == reviewID {
		t.Errorf("opening the pull request already moved the issue to review")
	}
	env.githubSend(ws.Id, secret, "pull_request", event("review_requested"))
	if got := ada.issue(ws.Id, one.Id); got.StateId != reviewID {
		t.Errorf("after review was requested the issue is in %s, want the review status", got.StateId)
	}
	// Again changes nothing.
	before := ada.lastSyncID(ws.Id)
	env.githubSend(ws.Id, secret, "pull_request", event("review_requested"))
	if after := ada.lastSyncID(ws.Id); after != before {
		t.Errorf("a repeated review request wrote %d sync actions", after-before)
	}

	// A commit on the default branch that says it fixes issues completes them.
	push := func(ref, message, author string) int {
		return env.githubSend(ws.Id, secret, "push", map[string]any{
			"ref": ref, "repository": repo, "sender": map[string]any{"login": "someone-else"},
			"commits": []any{map[string]any{"message": message, "author": map[string]any{"username": author}}},
		})
	}
	push("refs/heads/feature", "Fixes ACM-2", "ada-gh")
	if got := ada.issue(ws.Id, two.Id); got.CompletedAt != nil {
		t.Errorf("a push to a feature branch completed an issue")
	}
	push("refs/heads/main", "Tidy up, see ACM-3", "ada-gh") // a mention is not a fix
	if got := ada.issue(ws.Id, three.Id); got.CompletedAt != nil {
		t.Errorf("a commit that only mentions an issue completed it")
	}
	if got := push("refs/heads/main", "Logout and signup\n\nFixes ACM-2 and acm-3, closes XYZ-9.", "ada-gh"); got != http.StatusNoContent {
		t.Fatalf("push event: status %d", got)
	}
	for _, id := range []uuid.UUID{two.Id, three.Id} {
		if got := ada.issue(ws.Id, id); got.StateId != done.Id {
			t.Errorf("issue %d is in %s after the fixing commit, want done", got.Number, got.StateId)
		}
	}
	// Bob follows nothing here; ada moved her own: no notifications for it.
	if got := bob.notificationTypes(ws.Id); len(got) != 0 {
		t.Errorf("bob's notifications = %v, want none", got)
	}
	// It never moves an issue backwards.
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+two.Id.String(), map[string]any{"stateId": backlog.Id}, nil)
	env.githubSend(ws.Id, secret, "pull_request", event("review_requested"))
	if got := ada.issue(ws.Id, two.Id); got.StateId != backlog.Id {
		t.Errorf("an unrelated event moved an issue")
	}

	// Deleting the review status clears the team's setting for everyone.
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+one.Id.String(), map[string]any{"stateId": done.Id}, nil)
	ada.expect(http.StatusOK, "DELETE", "/api/states/"+reviewID.String(), nil, nil)
	for _, tm := range ada.bootstrap(ws.Id).Teams {
		if tm.Id == team.Id && tm.ReviewStateId != nil {
			t.Errorf("team still names the deleted review status")
		}
	}
}

func TestClosingRefs(t *testing.T) {
	cases := map[string][]issueRef{
		"Fixes ENG-12":                       {{"ENG", 12}},
		"fix: eng-12":                        {{"ENG", 12}},
		"Closes ENG-1, ENG-2 and DES-3.":     {{"ENG", 1}, {"ENG", 2}, {"DES", 3}},
		"Resolved ENG-4\n\nAlso fixed ENG-5": {{"ENG", 4}, {"ENG", 5}},
		"See ENG-12; relates to ENG-13":      nil,
		"prefixes ENG-12":                    nil,
		"Fixes the thing (ENG-12)":           nil,
	}
	for message, want := range cases {
		got := closingRefs(message)
		if len(got) != len(want) {
			t.Errorf("closingRefs(%q) = %v, want %v", message, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("closingRefs(%q) = %v, want %v", message, got, want)
			}
		}
	}
}
