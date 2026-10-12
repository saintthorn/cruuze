package server

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

func (c *client) createProject(workspaceID uuid.UUID, name string) api.Project {
	c.t.Helper()
	var out api.ProjectMutationResult
	c.expect(http.StatusCreated, "POST", "/api/workspaces/"+workspaceID.String()+"/projects", map[string]any{"id": uuid.New(), "name": name}, &out)
	return out.Project
}

func TestInitiatives(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, _ := workspaceWithMember(t, env)
	path := "/api/workspaces/" + ws.Id.String() + "/initiatives"

	id := uuid.New()
	body := map[string]any{"id": id, "name": "  Self-serve onboarding ", "targetDate": "2027-03-31"}
	ada.expect(http.StatusCreated, "POST", path, body, nil)
	ada.expect(http.StatusOK, "POST", path, body, nil) // a retry
	ada.expect(http.StatusBadRequest, "POST", path, map[string]any{"id": uuid.New(), "name": " "}, nil)
	ada.expect(http.StatusBadRequest, "POST", path, map[string]any{"id": uuid.New(), "name": "Red", "color": "red"}, nil)

	boot := bob.bootstrap(ws.Id)
	if len(boot.Initiatives) != 1 || boot.Initiatives[0].Name != "Self-serve onboarding" || boot.Initiatives[0].TargetDate == nil {
		t.Fatalf("initiatives = %+v", boot.Initiatives)
	}

	// Projects join it, on creation or later; not one from elsewhere.
	var made api.ProjectMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/projects", map[string]any{"id": uuid.New(), "name": "Signup", "initiativeId": id}, &made)
	if made.Project.InitiativeId == nil || *made.Project.InitiativeId != id {
		t.Errorf("created project's initiative = %v", made.Project.InitiativeId)
	}
	billing := ada.createProject(ws.Id, "Billing")
	var moved api.ProjectMutationResult
	bob.expect(http.StatusOK, "PATCH", "/api/projects/"+billing.Id.String(), map[string]any{"initiativeId": id}, &moved)
	if moved.Project.InitiativeId == nil {
		t.Error("the project didn't join the initiative")
	}
	ada.expect(http.StatusBadRequest, "PATCH", "/api/projects/"+billing.Id.String(), map[string]any{"initiativeId": uuid.New()}, nil)
	var left api.ProjectMutationResult
	ada.expect(http.StatusOK, "PATCH", "/api/projects/"+billing.Id.String(), map[string]any{"initiativeId": nil}, &left)
	if left.Project.InitiativeId != nil {
		t.Error("the project didn't leave the initiative")
	}
	// A change that says nothing about the initiative leaves it alone.
	var renamed api.ProjectMutationResult
	ada.expect(http.StatusOK, "PATCH", "/api/projects/"+made.Project.Id.String(), map[string]any{"name": "Sign-up"}, &renamed)
	if renamed.Project.InitiativeId == nil {
		t.Error("renaming the project took it out of its initiative")
	}

	ada.expect(http.StatusOK, "PATCH", "/api/initiatives/"+id.String(), map[string]any{"name": "Onboarding", "targetDate": nil}, nil)
	if got := ada.bootstrap(ws.Id).Initiatives[0]; got.Name != "Onboarding" || got.TargetDate != nil {
		t.Errorf("after the edit: %+v", got)
	}

	// Outsiders see nothing; deleting frees the projects, and says so.
	carol, _ := env.signIn(t, "carol@example.com")
	carol.expect(http.StatusNotFound, "PATCH", "/api/initiatives/"+id.String(), map[string]any{"name": "Mine"}, nil)
	cursor := ada.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "DELETE", "/api/initiatives/"+id.String(), nil, nil)
	boot = ada.bootstrap(ws.Id)
	if len(boot.Initiatives) != 0 || len(boot.Projects) != 2 {
		t.Fatalf("after deleting: %d initiatives, %d projects", len(boot.Initiatives), len(boot.Projects))
	}
	for _, p := range boot.Projects {
		if p.InitiativeId != nil {
			t.Errorf("project %s still has an initiative", p.Name)
		}
	}
	if after := ada.lastSyncID(ws.Id); after != cursor+2 {
		t.Errorf("deleting wrote %d sync actions, want 2 (the project, the initiative)", after-cursor)
	}
}

func TestProjectDependencies(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, _ := workspaceWithMember(t, env)
	api1, app, launch := ada.createProject(ws.Id, "API"), ada.createProject(ws.Id, "App"), ada.createProject(ws.Id, "Launch")
	depend := func(c *client, project, on api.Project, id uuid.UUID) int {
		return c.do("POST", "/api/projects/"+project.Id.String()+"/dependencies", map[string]any{"id": id, "dependsOnId": on.Id}, nil)
	}

	first := uuid.New()
	if status := depend(ada, app, api1, first); status != http.StatusCreated {
		t.Fatalf("app depends on api: status %d", status)
	}
	if status := depend(ada, app, api1, first); status != http.StatusOK {
		t.Errorf("a retry: status %d", status)
	}
	if status := depend(bob, app, api1, uuid.New()); status != http.StatusOK {
		t.Errorf("saying it twice: status %d", status)
	}
	if status := depend(ada, launch, app, uuid.New()); status != http.StatusCreated {
		t.Errorf("launch depends on app: status %d", status)
	}
	// No loops, however long.
	if status := depend(ada, app, app, uuid.New()); status != http.StatusBadRequest {
		t.Errorf("depending on itself: status %d", status)
	}
	if status := depend(ada, api1, app, uuid.New()); status != http.StatusConflict {
		t.Errorf("a loop of two: status %d", status)
	}
	if status := depend(ada, api1, launch, uuid.New()); status != http.StatusConflict {
		t.Errorf("a loop of three: status %d", status)
	}
	// Nor on a project elsewhere.
	carol, _ := env.signIn(t, "carol@example.com")
	other := carol.createProject(carol.createWorkspace("Other", "other-co").Id, "Theirs")
	if status := depend(ada, api1, other, uuid.New()); status != http.StatusBadRequest {
		t.Errorf("depending on another workspace's project: status %d", status)
	}
	if status := depend(carol, app, api1, uuid.New()); status != http.StatusNotFound {
		t.Errorf("an outsider adding a dependency: status %d", status)
	}

	if got := bob.bootstrap(ws.Id).ProjectDependencies; len(got) != 2 {
		t.Fatalf("dependencies = %+v", got)
	}
	carol.expect(http.StatusNotFound, "DELETE", "/api/project-dependencies/"+first.String(), nil, nil)
	ada.expect(http.StatusOK, "DELETE", "/api/project-dependencies/"+first.String(), nil, nil)
	// With that gone, the other direction is fine.
	if status := depend(ada, api1, app, uuid.New()); status != http.StatusCreated {
		t.Errorf("after removing it, the reverse: status %d", status)
	}
	// Deleting a project takes its dependencies with it.
	ada.expect(http.StatusOK, "DELETE", "/api/projects/"+app.Id.String(), nil, nil)
	if got := ada.bootstrap(ws.Id).ProjectDependencies; len(got) != 0 {
		t.Errorf("dependencies after deleting the project: %+v", got)
	}
}

func TestProjectUpdates(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, _ := workspaceWithMember(t, env)
	project := ada.createProject(ws.Id, "Launch")
	path := "/api/projects/" + project.Id.String() + "/updates"

	first := uuid.New()
	body := map[string]any{"id": first, "health": "atRisk", "body": " Waiting on legal. "}
	bob.expect(http.StatusCreated, "POST", path, body, nil)
	bob.expect(http.StatusOK, "POST", path, body, nil)
	bob.expect(http.StatusBadRequest, "POST", path, map[string]any{"id": uuid.New(), "health": "doomed"}, nil)
	ada.expect(http.StatusCreated, "POST", path, map[string]any{"id": uuid.New(), "health": "onTrack"}, nil)

	updates := ada.bootstrap(ws.Id).ProjectUpdates
	if len(updates) != 2 || updates[0].Health != api.ProjectHealthAtRisk || updates[0].Body != "Waiting on legal." ||
		updates[0].UserId == nil || *updates[0].UserId != bobMe.User.Id || updates[1].Health != api.ProjectHealthOnTrack {
		t.Fatalf("updates = %+v", updates)
	}

	// Only the latest few of each project are sent along.
	for range 12 {
		ada.expect(http.StatusCreated, "POST", path, map[string]any{"id": uuid.New(), "health": "onTrack"}, nil)
	}
	if got := len(ada.bootstrap(ws.Id).ProjectUpdates); got != 10 {
		t.Errorf("bootstrap has %d updates, want the latest 10", got)
	}

	// Its author or an admin may delete one.
	mine := uuid.New()
	ada.expect(http.StatusCreated, "POST", path, map[string]any{"id": mine, "health": "offTrack"}, nil)
	bob.expect(http.StatusForbidden, "DELETE", "/api/project-updates/"+mine.String(), nil, nil)
	ada.expect(http.StatusOK, "DELETE", "/api/project-updates/"+first.String(), nil, nil)
	ada.expect(http.StatusOK, "DELETE", "/api/project-updates/"+mine.String(), nil, nil)
	ada.expect(http.StatusNotFound, "DELETE", "/api/project-updates/"+mine.String(), nil, nil)
}
