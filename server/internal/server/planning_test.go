package server

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

// workspaceWithMember sets up a workspace owned by ada with bob as a member
// (of the workspace, not of any team).
func workspaceWithMember(t *testing.T, env *testEnv) (ada, bob *client, adaMe, bobMe api.Me, ws api.Workspace, team api.TeamRecord) {
	t.Helper()
	ada, adaMe = env.signIn(t, "ada@example.com")
	ws = ada.createWorkspace("Acme", "acme")
	member := api.WorkspaceRoleMember
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/invites", api.CreateInviteRequest{Email: "bob@example.com", Role: &member}, nil)
	bob, bobMe = env.signIn(t, "bob@example.com")
	return ada, bob, adaMe, bobMe, ws, ada.bootstrap(ws.Id).Teams[0]
}

func TestProjectsAndMilestones(t *testing.T) {
	env := newTestEnv(t)
	ada, _, adaMe, _, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()

	// Create, with an idempotent retry.
	req := map[string]any{"id": newID(), "name": " Launch ", "status": "planned", "targetDate": "2026-12-01", "leadId": adaMe.User.Id}
	var created, retried api.ProjectMutationResult
	ada.expect(http.StatusCreated, "POST", base+"/projects", req, &created)
	ada.expect(http.StatusOK, "POST", base+"/projects", req, &retried)
	project := created.Project
	if project.Name != "Launch" || project.Status != api.ProjectStatusPlanned || project.TargetDate == nil || retried.Project.Id != project.Id {
		t.Errorf("project = %+v", project)
	}
	ada.expect(http.StatusBadRequest, "POST", base+"/projects", map[string]any{"id": newID(), "name": "X", "status": "someday"}, nil)
	ada.expect(http.StatusBadRequest, "POST", base+"/projects", map[string]any{"id": newID(), "name": "X", "color": "red"}, nil)
	ada.expect(http.StatusBadRequest, "POST", base+"/projects", map[string]any{"id": newID(), "name": "X", "leadId": newID()}, nil)

	projectPath := "/api/projects/" + project.Id.String()
	var updated api.ProjectMutationResult
	ada.expect(http.StatusOK, "PATCH", projectPath, map[string]any{"status": "started", "leadId": nil, "startDate": "2026-10-01"}, &updated)
	if updated.Project.Status != api.ProjectStatusStarted || updated.Project.LeadId != nil || updated.Project.StartDate == nil || updated.Project.TargetDate == nil {
		t.Errorf("updated project = %+v", updated.Project)
	}

	var other api.ProjectMutationResult
	ada.expect(http.StatusCreated, "POST", base+"/projects", map[string]any{"id": newID(), "name": "Other"}, &other)
	var beta, foreign api.MilestoneMutationResult
	ada.expect(http.StatusCreated, "POST", projectPath+"/milestones", map[string]any{"id": newID(), "name": "Beta", "targetDate": "2026-11-01"}, &beta)
	ada.expect(http.StatusCreated, "POST", "/api/projects/"+other.Project.Id.String()+"/milestones", map[string]any{"id": newID(), "name": "Elsewhere"}, &foreign)

	// An issue joins the project and one of its milestones, not another's.
	issuesPath := "/api/teams/" + team.Id.String() + "/issues"
	var planned api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", issuesPath, map[string]any{"id": newID(), "title": "Ship", "projectId": project.Id, "milestoneId": beta.Milestone.Id}, &planned)
	if planned.Issue.ProjectId == nil || planned.Issue.MilestoneId == nil {
		t.Fatalf("issue = %+v", planned.Issue)
	}
	issuePath := "/api/issues/" + planned.Issue.Id.String()
	ada.expect(http.StatusBadRequest, "PATCH", issuePath, map[string]any{"milestoneId": foreign.Milestone.Id}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", issuePath, map[string]any{"projectId": newID()}, nil)
	ada.expect(http.StatusBadRequest, "POST", issuesPath, map[string]any{"id": newID(), "title": "x", "milestoneId": beta.Milestone.Id}, nil)

	// Moving to another project drops the old project's milestone.
	var moved api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"projectId": other.Project.Id}, &moved)
	if moved.Issue.ProjectId == nil || *moved.Issue.ProjectId != other.Project.Id || moved.Issue.MilestoneId != nil {
		t.Errorf("after moving project: %+v", moved.Issue)
	}
	var kinds []api.ActivityType
	for _, a := range ada.details(issuePath).Activities {
		kinds = append(kinds, a.Type)
	}
	// Moving to another project also drops the old project's milestone.
	if len(kinds) != 3 || kinds[1] != api.ActivityTypeProject || kinds[2] != api.ActivityTypeMilestone {
		t.Errorf("activities = %v, want created, project, milestone", kinds)
	}

	// Deleting a project leaves its issues, without a project.
	ada.expect(http.StatusOK, "DELETE", "/api/projects/"+other.Project.Id.String(), nil, nil)
	boot := ada.bootstrap(ws.Id)
	if len(boot.Projects) != 1 || len(boot.Milestones) != 1 || len(boot.Issues) != 1 || boot.Issues[0].ProjectId != nil {
		t.Errorf("after deleting a project: %d projects, %d milestones, issue project %v", len(boot.Projects), len(boot.Milestones), boot.Issues[0].ProjectId)
	}
	ada.expect(http.StatusOK, "PATCH", "/api/milestones/"+beta.Milestone.Id.String(), map[string]any{"name": "Beta 2", "targetDate": nil}, nil)
	ada.expect(http.StatusOK, "DELETE", "/api/milestones/"+beta.Milestone.Id.String(), nil, nil)

	// Guests can't manage projects; outsiders can't see them.
	guest := api.WorkspaceRoleGuest
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "gus@example.com", Role: &guest}, nil)
	gus, _ := env.signIn(t, "gus@example.com")
	gus.expect(http.StatusForbidden, "POST", base+"/projects", map[string]any{"id": newID(), "name": "Mine"}, nil)
	gus.expect(http.StatusForbidden, "DELETE", projectPath, nil, nil)
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "POST", base+"/projects", map[string]any{"id": newID(), "name": "Mine"}, nil)
	eve.expect(http.StatusNotFound, "PATCH", projectPath, map[string]any{"name": "Mine"}, nil)
}

func TestSubIssuesAndRelations(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()

	parent := ada.createIssue(team.Id, "Parent").Issue
	child := ada.createIssue(team.Id, "Child").Issue
	grandchild := ada.createIssue(team.Id, "Grandchild").Issue
	path := func(i api.Issue) string { return "/api/issues/" + i.Id.String() }

	var result api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", path(child), map[string]any{"parentId": parent.Id}, &result)
	if result.Issue.ParentId == nil || *result.Issue.ParentId != parent.Id {
		t.Errorf("child parent = %v", result.Issue.ParentId)
	}
	ada.expect(http.StatusOK, "PATCH", path(grandchild), map[string]any{"parentId": child.Id}, nil)
	// No issue can be its own ancestor.
	ada.expect(http.StatusBadRequest, "PATCH", path(parent), map[string]any{"parentId": parent.Id}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", path(parent), map[string]any{"parentId": grandchild.Id}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", path(parent), map[string]any{"parentId": newID()}, nil)
	var detached api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", path(child), map[string]any{"parentId": nil}, &detached)
	if detached.Issue.ParentId != nil {
		t.Errorf("parent not cleared: %v", detached.Issue.ParentId)
	}

	// Relations, with an idempotent retry and no duplicates.
	req := api.CreateRelationRequest{Id: newID(), RelatedIssueId: child.Id, Type: api.RelationTypeBlocks}
	var relation api.RelationMutationResult
	ada.expect(http.StatusCreated, "POST", path(parent)+"/relations", req, &relation)
	ada.expect(http.StatusOK, "POST", path(parent)+"/relations", req, nil)
	ada.expect(http.StatusConflict, "POST", path(parent)+"/relations", api.CreateRelationRequest{Id: newID(), RelatedIssueId: child.Id, Type: api.RelationTypeBlocks}, nil)
	ada.expect(http.StatusCreated, "POST", path(parent)+"/relations", api.CreateRelationRequest{Id: newID(), RelatedIssueId: child.Id, Type: api.RelationTypeRelated}, nil)
	ada.expect(http.StatusBadRequest, "POST", path(parent)+"/relations", api.CreateRelationRequest{Id: newID(), RelatedIssueId: parent.Id, Type: api.RelationTypeRelated}, nil)
	ada.expect(http.StatusBadRequest, "POST", path(parent)+"/relations", map[string]any{"id": newID(), "relatedIssueId": child.Id, "type": "enemy"}, nil)

	// You can't relate to, or nest under, an issue you can't see.
	private := true
	var secretTeam api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secretTeam)
	secret := ada.createIssue(secretTeam.Id, "Secret").Issue
	bobIssue := bob.createIssue(team.Id, "Bob's").Issue
	bob.expect(http.StatusBadRequest, "POST", path(bobIssue)+"/relations", api.CreateRelationRequest{Id: newID(), RelatedIssueId: secret.Id, Type: api.RelationTypeRelated}, nil)
	bob.expect(http.StatusBadRequest, "PATCH", path(bobIssue), map[string]any{"parentId": secret.Id}, nil)

	if got := ada.bootstrap(ws.Id).Relations; len(got) != 2 {
		t.Errorf("%d relations, want 2", len(got))
	}
	ada.expect(http.StatusOK, "DELETE", "/api/relations/"+relation.Relation.Id.String(), nil, nil)
	ada.expect(http.StatusNotFound, "DELETE", "/api/relations/"+relation.Relation.Id.String(), nil, nil)
}

func TestCycles(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	teamPath := "/api/teams/" + team.Id.String()

	// Only the team (or an admin) changes its settings.
	bob.expect(http.StatusForbidden, "PATCH", teamPath, map[string]any{"cycleWeeks": 2}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", teamPath, map[string]any{"cycleWeeks": 9}, nil)

	var settings api.TeamMutationResult
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"cycleWeeks": 2}, &settings)
	if settings.Team.CycleWeeks != 2 {
		t.Fatalf("cycleWeeks = %d", settings.Team.CycleWeeks)
	}

	// Turning cycles on creates the current cycle and the next two, back to back.
	cycles := ada.bootstrap(ws.Id).Cycles
	if len(cycles) != 3 {
		t.Fatalf("%d cycles, want 3", len(cycles))
	}
	today := dateOf(time.Now())
	first := cycles[0]
	if first.Number != 1 || first.StartsAt.Weekday() != time.Monday || first.StartsAt.After(today) || first.EndsAt.Before(today) {
		t.Errorf("first cycle = %+v, want one starting on a Monday that contains today", first)
	}
	if days := first.EndsAt.Sub(first.StartsAt.Time).Hours() / 24; days != 13 {
		t.Errorf("first cycle spans %v days after its start, want 13 (two weeks)", days)
	}
	for i := 1; i < len(cycles); i++ {
		if !cycles[i].StartsAt.Equal(cycles[i-1].EndsAt.AddDate(0, 0, 1)) || cycles[i].Number != cycles[i-1].Number+1 {
			t.Errorf("cycle %d doesn't follow cycle %d: %+v", i+1, i, cycles[i])
		}
	}

	// Issues join a cycle of their own team only.
	boot := ada.bootstrap(ws.Id)
	var open, done api.IssueMutationResult
	issuesPath := teamPath + "/issues"
	ada.expect(http.StatusCreated, "POST", issuesPath, map[string]any{"id": newID(), "title": "Unfinished", "cycleId": first.Id}, &open)
	ada.expect(http.StatusCreated, "POST", issuesPath, map[string]any{"id": newID(), "title": "Finished", "cycleId": first.Id, "stateId": boot.WorkflowStates[3].Id}, &done)
	var design api.Team
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/teams", api.CreateTeamRequest{Name: "Design", Key: "DES"}, &design)
	ada.expect(http.StatusBadRequest, "POST", "/api/teams/"+design.Id.String()+"/issues", map[string]any{"id": newID(), "title": "x", "cycleId": first.Id}, nil)

	// Upkeep today changes nothing.
	ctx := context.Background()
	before := ada.lastSyncID(ws.Id)
	if err := env.srv.keepAllCycles(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if after := ada.lastSyncID(ws.Id); after != before {
		t.Errorf("upkeep with nothing to do moved the log from %d to %d", before, after)
	}

	// The day after the first cycle ends, unfinished work rolls into the next
	// one, finished work stays, and another upcoming cycle is added.
	if err := env.srv.keepAllCycles(ctx, first.EndsAt.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	after := ada.bootstrap(ws.Id)
	if len(after.Cycles) != 4 {
		t.Errorf("%d cycles after rollover, want 4", len(after.Cycles))
	}
	cycleOf := map[uuid.UUID]*uuid.UUID{}
	for _, i := range after.Issues {
		cycleOf[i.Id] = i.CycleId
	}
	if got := cycleOf[open.Issue.Id]; got == nil || *got != cycles[1].Id {
		t.Errorf("unfinished issue is in cycle %v, want the second cycle %v", got, cycles[1].Id)
	}
	if got := cycleOf[done.Issue.Id]; got == nil || *got != first.Id {
		t.Errorf("finished issue is in cycle %v, want to stay in the first", got)
	}
	activities := ada.details("/api/issues/" + open.Issue.Id.String()).Activities
	last := activities[len(activities)-1]
	if last.Type != api.ActivityTypeCycle || last.ActorId != nil || last.IssueId != open.Issue.Id {
		t.Errorf("rollover activity = %+v, want a cycle change with no actor", last)
	}
	// Running it again for the same day does nothing more.
	before = ada.lastSyncID(ws.Id)
	if err := env.srv.keepAllCycles(ctx, first.EndsAt.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if got := ada.lastSyncID(ws.Id); got != before {
		t.Errorf("repeated upkeep moved the log from %d to %d", before, got)
	}
}

func TestTriage(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	teamPath := "/api/teams/" + team.Id.String()

	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"triageEnabled": true}, nil)
	ada.expect(http.StatusOK, "PATCH", teamPath, map[string]any{"triageEnabled": true}, nil)
	var triage *api.WorkflowState
	states := ada.bootstrap(ws.Id).WorkflowStates
	for i := range states {
		if states[i].Type == api.WorkflowStateTypeTriage {
			if triage != nil {
				t.Fatal("more than one triage state")
			}
			triage = &states[i]
		}
	}
	if triage == nil || len(states) != 6 {
		t.Fatalf("states = %+v, want the default five plus triage", states)
	}

	// Someone outside the team files into triage; the team's own issues skip it.
	outside := bob.createIssue(team.Id, "From outside").Issue
	inside := ada.createIssue(team.Id, "From the team").Issue
	if outside.StateId != triage.Id {
		t.Errorf("an outsider's issue landed in %v, want triage", outside.StateId)
	}
	if inside.StateId == triage.Id {
		t.Error("a team member's issue landed in triage")
	}
	// Accepting it is an ordinary status change.
	var accepted api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+outside.Id.String(), map[string]any{"stateId": inside.StateId}, &accepted)
	if accepted.Issue.StateId != inside.StateId {
		t.Errorf("accepted issue state = %v", accepted.Issue.StateId)
	}
}

func TestTemplatesViewsAndFavorites(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, bobMe, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()

	// Templates belong to a team.
	var template api.TemplateMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/templates", map[string]any{"id": newID(), "name": "Bug report", "title": "Bug: ", "priority": 2}, &template)
	templatePath := "/api/templates/" + template.Template.Id.String()
	ada.expect(http.StatusOK, "PATCH", templatePath, map[string]any{"description": "## Steps"}, &template)
	if template.Template.Title != "Bug:" || template.Template.Priority != 2 || template.Template.Description != "## Steps" {
		t.Errorf("template = %+v", template.Template)
	}
	ada.expect(http.StatusBadRequest, "PATCH", templatePath, map[string]any{"priority": 7}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", templatePath, map[string]any{"labelIds": []any{newID()}}, nil)
	if got := bob.bootstrap(ws.Id).Templates; len(got) != 1 {
		t.Errorf("a member sees %d templates, want 1", len(got))
	}

	// A private view is the owner's alone.
	var view api.ViewMutationResult
	options := map[string]any{"grouping": "priority", "filter": map[string]any{"priorities": []any{1, 2}}}
	ada.expect(http.StatusCreated, "POST", base+"/views", map[string]any{"id": newID(), "name": "Hot", "options": options}, &view)
	viewPath := "/api/views/" + view.View.Id.String()
	if view.View.Shared || view.View.OwnerId != adaMe.User.Id || view.View.Options["grouping"] != "priority" {
		t.Errorf("view = %+v", view.View)
	}
	if got := bob.bootstrap(ws.Id).Views; len(got) != 0 {
		t.Errorf("bob sees %d of ada's private views", len(got))
	}
	bob.expect(http.StatusNotFound, "PATCH", viewPath, map[string]any{"name": "Mine"}, nil)

	// Sharing hands it to everyone; unsharing takes it back.
	cursor := bob.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", viewPath, map[string]any{"shared": true}, nil)
	shared := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, cursor)
	if got := only(shared, api.SyncModelView); len(got) != 1 || got[0] != "I:View" {
		t.Errorf("after sharing, bob's log = %v, want the view inserted", models(shared))
	}
	bob.expect(http.StatusForbidden, "PATCH", viewPath, map[string]any{"name": "Mine"}, nil)
	bob.expect(http.StatusForbidden, "DELETE", viewPath, nil, nil)
	ada.expect(http.StatusOK, "PATCH", viewPath, map[string]any{"shared": false, "name": "Hot stuff"}, nil)
	unshared := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, shared.Cursor)
	if got := only(unshared, api.SyncModelView); len(got) != 1 || got[0] != "D:View" {
		t.Errorf("after unsharing, bob's log = %v, want the view deleted", models(unshared))
	}
	if got := ada.bootstrap(ws.Id).Views; len(got) != 1 || got[0].Name != "Hot stuff" {
		t.Errorf("ada's views = %+v", got)
	}

	// Favorites are private, and one per target.
	issue := ada.createIssue(team.Id, "Starred").Issue
	var favorite, again api.FavoriteMutationResult
	ada.expect(http.StatusCreated, "POST", base+"/favorites", map[string]any{"id": newID(), "kind": "issue", "targetId": issue.Id}, &favorite)
	ada.expect(http.StatusOK, "POST", base+"/favorites", map[string]any{"id": newID(), "kind": "issue", "targetId": issue.Id}, &again)
	if again.Favorite.Id != favorite.Favorite.Id {
		t.Error("favoriting the same thing twice made two favorites")
	}
	ada.expect(http.StatusBadRequest, "POST", base+"/favorites", map[string]any{"id": newID(), "kind": "team", "targetId": team.Id}, nil)
	if got := bob.bootstrap(ws.Id).Favorites; len(got) != 0 {
		t.Errorf("bob sees %d of ada's favorites", len(got))
	}
	favoritePath := "/api/favorites/" + favorite.Favorite.Id.String()
	bob.expect(http.StatusNotFound, "DELETE", favoritePath, nil, nil)
	ada.expect(http.StatusOK, "DELETE", favoritePath, nil, nil)
	ada.expect(http.StatusOK, "DELETE", templatePath, nil, nil)
	ada.expect(http.StatusOK, "DELETE", viewPath, nil, nil)
	if boot := ada.bootstrap(ws.Id); len(boot.Favorites)+len(boot.Templates)+len(boot.Views) != 0 {
		t.Errorf("leftovers after deleting: %d favorites, %d templates, %d views", len(boot.Favorites), len(boot.Templates), len(boot.Views))
	}
}

func TestCycleDates(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for in, want := range map[string]string{
		"2026-10-01": "2026-09-28", // a Thursday
		"2026-09-28": "2026-09-28", // a Monday
		"2026-10-04": "2026-09-28", // a Sunday
	} {
		if got := mondayOf(day(in)); !got.Equal(day(want)) {
			t.Errorf("mondayOf(%s) = %s, want %s", in, got.Format("2006-01-02"), want)
		}
	}
	late := time.Date(2026, 10, 1, 23, 30, 0, 0, time.FixedZone("west", -8*3600))
	if got := dateOf(late); !got.Equal(day("2026-10-02")) {
		t.Errorf("dateOf uses %s, want the UTC day 2026-10-02", got.Format("2006-01-02"))
	}
}

func TestMilestoneAlone(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	team := ada.bootstrap(ws.Id).Teams[0]

	var project api.ProjectMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/projects", map[string]any{"id": newID(), "name": "Launch"}, &project)
	var beta api.MilestoneMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/projects/"+project.Project.Id.String()+"/milestones", map[string]any{"id": newID(), "name": "Beta"}, &beta)
	var created api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/issues",
		map[string]any{"id": newID(), "title": "Stage me", "projectId": project.Project.Id}, &created)
	path := "/api/issues/" + created.Issue.Id.String()

	// A change to the milestone and nothing else is still a change.
	var set api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", path, map[string]any{"milestoneId": beta.Milestone.Id}, &set)
	if set.Issue.MilestoneId == nil || *set.Issue.MilestoneId != beta.Milestone.Id || set.SyncId <= created.SyncId {
		t.Errorf("after setting only the milestone: %+v", set)
	}
	var cleared api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", path, map[string]any{"milestoneId": nil}, &cleared)
	if cleared.Issue.MilestoneId != nil {
		t.Errorf("after clearing the milestone: %+v", cleared.Issue)
	}
	var kinds []api.ActivityType
	for _, a := range ada.details(path).Activities {
		kinds = append(kinds, a.Type)
	}
	if len(kinds) != 3 || kinds[1] != api.ActivityTypeMilestone || kinds[2] != api.ActivityTypeMilestone {
		t.Errorf("activities = %v, want created and two milestone changes", kinds)
	}
}

func TestDeleteTeam(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	ctx := context.Background()
	var second api.Team
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/teams", map[string]any{"name": "Wellness", "key": "WEL"}, &second)
	issue := ada.createIssue(second.Id, "Goes with the team")
	kept := ada.createIssue(team.Id, "Stays")

	// Members can't; admins can, and everything the team owned goes with it.
	bob.expect(http.StatusForbidden, "DELETE", "/api/teams/"+second.Id.String(), nil, nil)
	ada.expect(http.StatusNoContent, "DELETE", "/api/teams/"+second.Id.String(), nil, nil)
	ada.expect(http.StatusNotFound, "DELETE", "/api/teams/"+second.Id.String(), nil, nil)
	teams, err := env.q.ListTeamsOfWorkspace(ctx, ws.Id)
	if err != nil || len(teams) != 1 || teams[0].ID != team.Id {
		t.Fatalf("teams left = %v (%v), want only %s", teams, err, team.Key)
	}
	if _, err := env.q.GetIssue(ctx, issue.Issue.Id); !db.IsNoRows(err) {
		t.Errorf("the deleted team's issue is still there: %v", err)
	}
	if _, err := env.q.GetIssue(ctx, kept.Issue.Id); err != nil {
		t.Errorf("another team's issue is gone: %v", err)
	}
	// Clients are told to load the workspace afresh.
	actions, err := env.q.ListSyncActionsAfter(ctx, dbgen.ListSyncActionsAfterParams{WorkspaceID: ws.Id, SyncID: 0, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(actions, func(a dbgen.ListSyncActionsAfterRow) bool { return a.Model == syncer.ResetModel }) {
		t.Error("no reset in the sync log after deleting a team")
	}
}
