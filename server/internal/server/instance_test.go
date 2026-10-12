package server

import (
	"net/http"
	"testing"

	"cruuze/server/internal/api"
)

func TestTeamKinds(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	teamsPath := "/api/workspaces/" + ws.Id.String() + "/teams"

	names := func(kind string, key string) (api.TeamRecord, []string) {
		t.Helper()
		var team api.Team
		ada.expect(http.StatusCreated, "POST", teamsPath, map[string]any{"name": kind, "key": key, "kind": kind}, &team)
		boot := ada.bootstrap(ws.Id)
		var record api.TeamRecord
		for _, candidate := range boot.Teams {
			if candidate.Id == team.Id {
				record = candidate
			}
		}
		var out []string
		for _, s := range boot.WorkflowStates {
			if s.TeamId == team.Id {
				out = append(out, s.Name+":"+string(s.Type))
			}
		}
		return record, out
	}

	// The first team of a workspace is an engineering one.
	if first := ada.bootstrap(ws.Id).Teams[0]; first.Kind != api.TeamKindEngineering || first.TriageEnabled {
		t.Errorf("first team = %+v", first)
	}
	support, states := names("support", "SUP")
	if support.Kind != api.TeamKindSupport || !support.TriageEnabled {
		t.Errorf("support team = %+v", support)
	}
	want := []string{"Triage:triage", "Open:unstarted", "In Progress:started", "Waiting on Customer:started", "Resolved:completed", "Won't Fix:canceled"}
	if len(states) != len(want) {
		t.Fatalf("support statuses = %v, want %v", states, want)
	}
	for _, name := range want {
		found := false
		for _, got := range states {
			found = found || got == name
		}
		if !found {
			t.Errorf("support team has no %q status: %v", name, states)
		}
	}
	if general, states := names("general", "OPS"); general.Kind != api.TeamKindGeneral || len(states) != 4 || general.TriageEnabled {
		t.Errorf("general team = %+v with %v", general, states)
	}
	if _, states := names("marketing", "MKT"); len(states) != 6 {
		t.Errorf("marketing statuses = %v", states)
	}
	ada.expect(http.StatusBadRequest, "POST", teamsPath, map[string]any{"name": "Sales", "key": "SAL", "kind": "sales"}, nil)

	// Changing the kind keeps the statuses.
	var changed api.TeamMutationResult
	ada.expect(http.StatusOK, "PATCH", "/api/teams/"+support.Id.String(), map[string]any{"kind": "design"}, &changed)
	if changed.Team.Kind != api.TeamKindDesign {
		t.Errorf("after changing kind: %+v", changed.Team)
	}
	ada.expect(http.StatusBadRequest, "PATCH", "/api/teams/"+support.Id.String(), map[string]any{"kind": "sales"}, nil)
	count := 0
	for _, s := range ada.bootstrap(ws.Id).WorkflowStates {
		if s.TeamId == support.Id {
			count++
		}
	}
	if count != len(want) {
		t.Errorf("%d statuses after changing kind, want %d", count, len(want))
	}
}

func TestInstanceAdmin(t *testing.T) {
	env := newTestEnv(t)
	instance := func(c *client) api.InstanceSettings {
		t.Helper()
		var out api.InstanceSettings
		c.expect(http.StatusOK, "GET", "/api/admin/instance", nil, &out)
		return out
	}
	link := func(email string, extra ...string) int {
		body := map[string]any{"email": email}
		if len(extra) > 0 {
			body["portal"] = extra[0]
		}
		return env.client(t).do("POST", "/api/auth/magic-link", body, nil)
	}

	// Whoever signs in first runs the install; nobody after does.
	ada, adaMe := env.signIn(t, "ada@example.com")
	bob, bobMe := env.signIn(t, "bob@example.com")
	if !adaMe.InstanceAdmin || bobMe.InstanceAdmin || !adaMe.CanCreateWorkspace || !bobMe.CanCreateWorkspace {
		t.Fatalf("ada = %+v, bob = %+v", adaMe, bobMe)
	}
	bob.expect(http.StatusForbidden, "GET", "/api/admin/instance", nil, nil)
	bob.expect(http.StatusForbidden, "PATCH", "/api/admin/instance", map[string]any{"signup": "invited"}, nil)
	env.client(t).expect(http.StatusUnauthorized, "GET", "/api/admin/instance", nil, nil)
	ws := ada.createWorkspace("Acme", "acme")
	if got := instance(ada); got.Signup != api.SignupModeOpen || got.Users != 2 || got.Workspaces != 1 || len(got.Admins) != 1 || got.Admins[0] != "ada@example.com" || got.Version == "" {
		t.Errorf("instance = %+v", got)
	}

	// Invite-only: known and invited addresses get in, strangers don't.
	var settings api.InstanceSettings
	ada.expect(http.StatusOK, "PATCH", "/api/admin/instance", map[string]any{"signup": "invited"}, &settings)
	if settings.Signup != api.SignupModeInvited {
		t.Fatalf("after closing signups: %+v", settings)
	}
	var config api.AuthConfig
	env.client(t).expect(http.StatusOK, "GET", "/api/auth/config", nil, &config)
	if config.Signup != api.SignupModeInvited {
		t.Errorf("auth config = %+v", config)
	}
	if status := link("stranger@example.com"); status != http.StatusForbidden {
		t.Errorf("a stranger asking for a link: status %d, want 403", status)
	}
	if status := link("bob@example.com"); status != http.StatusNoContent {
		t.Errorf("someone with an account: status %d", status)
	}
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/invites", map[string]any{"email": "carol@example.com", "role": "member"}, nil)
	if status := link("carol@example.com"); status != http.StatusNoContent {
		t.Errorf("someone invited: status %d", status)
	}

	// Named domains get in too.
	ada.expect(http.StatusBadRequest, "PATCH", "/api/admin/instance", map[string]any{"signupDomains": []string{"not a domain"}}, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/admin/instance", map[string]any{"signup": "domains", "signupDomains": []string{" @Corp.example ", "corp.example"}}, &settings)
	if len(settings.SignupDomains) != 1 || settings.SignupDomains[0] != "corp.example" {
		t.Errorf("domains = %v", settings.SignupDomains)
	}
	if status := link("dan@corp.example"); status != http.StatusNoContent {
		t.Errorf("an allowed domain: status %d", status)
	}
	if status := link("dan@other.example"); status != http.StatusForbidden {
		t.Errorf("another domain: status %d", status)
	}

	// A customer still gets into an open portal, but can't start a workspace.
	ada.expect(http.StatusOK, "PATCH", "/api/workspaces/"+ws.Id.String()+"/portal", map[string]any{"enabled": true}, nil)
	if status := link("cust@elsewhere.example", "acme"); status != http.StatusNoContent {
		t.Fatalf("a customer asking for a portal link: status %d", status)
	}
	cust := env.client(t)
	var custMe api.Me
	token := tokenPattern.FindStringSubmatch(env.mail.lastTo(t, "cust@elsewhere.example").Text)[1]
	cust.expect(http.StatusOK, "POST", "/api/auth/verify", api.VerifyRequest{Token: token}, &custMe)
	if custMe.CanCreateWorkspace || custMe.InstanceAdmin {
		t.Errorf("customer = %+v", custMe)
	}
	cust.expect(http.StatusForbidden, "POST", "/api/workspaces", api.CreateWorkspaceRequest{Name: "Squat", UrlKey: "squat"}, nil)
	// A member of a workspace still can.
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/invites", map[string]any{"email": "bob@example.com", "role": "member"}, nil)
	bob, bobMe = env.signIn(t, "bob@example.com")
	if !bobMe.CanCreateWorkspace {
		t.Error("a workspace member can't create a workspace")
	}

	// Unless only admins may.
	ada.expect(http.StatusBadRequest, "PATCH", "/api/admin/instance", map[string]any{"workspaceCreation": "nobody"}, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/admin/instance", map[string]any{"workspaceCreation": "admins"}, nil)
	bob.expect(http.StatusForbidden, "POST", "/api/workspaces", api.CreateWorkspaceRequest{Name: "Bob's", UrlKey: "bobs"}, nil)
	ada.createWorkspace("Second", "second")

	// INSTANCE_ADMINS names admins from outside, on installs that predate this.
	env.srv.cfg.InstanceAdmins = []string{"bob@example.com", "eve@example.com"}
	if got := instance(bob); len(got.Admins) != 3 {
		t.Errorf("admins = %v", got.Admins)
	}
	if status := link("eve@example.com"); status != http.StatusNoContent {
		t.Errorf("a named admin signing up: status %d", status)
	}
}
