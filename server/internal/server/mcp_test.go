package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

// assistant talks to /api/mcp the way an MCP client does.
type assistant struct {
	t   *testing.T
	env *testEnv
	key string
	id  int
}

func (e *testEnv) assistant(t *testing.T, c *client, workspaceID uuid.UUID) *assistant {
	t.Helper()
	var created api.CreatedApiKey
	c.expect(http.StatusCreated, "POST", "/api/workspaces/"+workspaceID.String()+"/api-keys", api.CreateApiKeyRequest{Name: "Assistant"}, &created)
	return &assistant{t: t, env: e, key: created.Token}
}

// rpc sends one JSON-RPC request and returns its result.
func (a *assistant) rpc(method string, params any) map[string]any {
	a.t.Helper()
	a.id++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": a.id, "method": method, "params": params})
	res := a.env.send("POST", "/api/mcp", body, "Content-Type", "application/json", "Authorization", "Bearer "+a.key)
	if res.StatusCode != http.StatusOK {
		a.t.Fatalf("%s: status %d", method, res.StatusCode)
	}
	var out struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	decode(a.t, res, &out)
	if out.Error != nil {
		a.t.Fatalf("%s: %v", method, out.Error)
	}
	return out.Result
}

// call runs a tool and returns its text and whether it reported an error.
func (a *assistant) call(tool string, args map[string]any) (string, bool) {
	a.t.Helper()
	result := a.rpc("tools/call", map[string]any{"name": tool, "arguments": args})
	isError, _ := result["isError"].(bool)
	return result["content"].([]any)[0].(map[string]any)["text"].(string), isError
}

// ok runs a tool that must succeed and decodes its JSON answer into out.
func (a *assistant) ok(tool string, args map[string]any, out any) string {
	a.t.Helper()
	text, isError := a.call(tool, args)
	if isError {
		a.t.Fatalf("%s(%v) failed: %s", tool, args, text)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			a.t.Fatalf("%s answered %q: %v", tool, text, err)
		}
	}
	return text
}

// fails runs a tool that must report an error mentioning each of the words.
func (a *assistant) fails(tool string, args map[string]any, words ...string) {
	a.t.Helper()
	text, isError := a.call(tool, args)
	if !isError {
		a.t.Errorf("%s(%v) succeeded: %s", tool, args, text)
	}
	for _, word := range words {
		if !strings.Contains(text, word) {
			a.t.Errorf("%s(%v) said %q, want it to mention %q", tool, args, text, word)
		}
	}
}

type toolIssue struct {
	Ref, Title, Status, StatusKind, Priority, Assignee, Project, Due, URL string
	Labels                                                                []string
}

func TestMCP(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, team := workspaceWithMember(t, env)
	claude := env.assistant(t, ada, ws.Id)
	// People without a name go by their address.
	bobName := bobMe.User.Name
	if bobName == "" {
		bobName = bobMe.User.Email
	}

	// It needs an API key: not nothing, and not a browser session.
	if res := env.send("POST", "/api/mcp", []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a key: status %d", res.StatusCode)
	}
	if status := ada.do("POST", "/api/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, nil); status != http.StatusForbidden {
		t.Errorf("with a browser session: status %d", status)
	}

	// The handshake, and what is on offer.
	if init := claude.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18"}); init["protocolVersion"] != "2025-06-18" || !strings.Contains(init["instructions"].(string), "get_context") {
		t.Errorf("initialize = %v", init)
	}
	tools := claude.rpc("tools/list", nil)["tools"].([]any)
	names := map[string]bool{}
	for _, tool := range tools {
		entry := tool.(map[string]any)
		names[entry["name"].(string)] = true
		if entry["description"] == "" || entry["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("tool %v is not fully described", entry["name"])
		}
	}
	for _, want := range []string{"get_context", "list_issues", "search_issues", "get_issue", "create_issue", "update_issue", "add_comment", "log_time", "get_project", "post_project_update", "team_insights", "list_customer_requests"} {
		if !names[want] {
			t.Errorf("no %s tool", want)
		}
	}

	// The vocabulary.
	var world struct {
		Workspace string
		Me        struct{ Email, Role string }
		Teams     []struct {
			Key      string
			Statuses []struct{ Name, Kind string }
		}
		People []struct{ Name, Email string }
	}
	claude.ok("get_context", nil, &world)
	if world.Workspace != ws.Name || world.Me.Email != "ada@example.com" || world.Me.Role != "admin" || len(world.Teams) != 1 || world.Teams[0].Key != team.Key || len(world.Teams[0].Statuses) != 5 || len(world.People) != 2 {
		t.Fatalf("context = %+v", world)
	}

	// Creating, with names rather than IDs.
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/labels", map[string]any{"id": uuid.New(), "name": "Bug", "color": "#eb5757"}, nil)
	project := ada.createProject(ws.Id, "Launch")
	var made toolIssue
	claude.ok("create_issue", map[string]any{
		"title": "Login fails on Safari", "description": "Steps:\n1. Open Safari", "priority": "high",
		"assignee": "bob", "labels": []string{"bug"}, "project": "launch", "dueDate": "2026-12-01",
	}, &made)
	if made.Ref != team.Key+"-1" || made.Status != "Backlog" || made.Priority != "high" || made.Assignee != bobName || made.Project != "Launch" ||
		len(made.Labels) != 1 || made.Labels[0] != "Bug" || made.Due != "2026-12-01" || !strings.HasSuffix(made.URL, "/issue/"+made.Ref) {
		t.Errorf("created = %+v", made)
	}
	// It is a real issue, made by the key's owner, and bob was told.
	stored := bob.issue(ws.Id, uuid.MustParse(ada.bootstrap(ws.Id).Issues[0].Id.String()))
	if stored.Title != "Login fails on Safari" || stored.ProjectId == nil || *stored.ProjectId != project.Id {
		t.Errorf("stored issue = %+v", stored)
	}
	if got := bob.notificationTypes(ws.Id); !sameTypes(got, api.NotificationTypeAssigned) {
		t.Errorf("bob's notifications = %v, want assigned", got)
	}

	// A wrong name is answered with the right ones.
	claude.fails("create_issue", map[string]any{"title": "x", "status": "Doing"}, "Backlog", "In Progress")
	claude.fails("create_issue", map[string]any{"title": "x", "assignee": "carol"}, "ada@example.com")
	claude.fails("create_issue", map[string]any{"title": "x", "labels": []string{"Chore"}}, "Bug")
	claude.fails("create_issue", map[string]any{"title": "x", "team": "OPS"}, team.Key)
	claude.fails("create_issue", map[string]any{"title": "x", "priorty": "high"}, "priorty")
	claude.fails("create_issue", map[string]any{"title": " "}, "title")
	claude.fails("get_issue", map[string]any{"ref": "NOPE-99"}, "No issue")

	// Changing: only what is named changes, and "none" clears.
	var changed toolIssue
	claude.ok("update_issue", map[string]any{"ref": made.Ref, "status": "in progress", "assignee": "me", "dueDate": "none", "removeLabels": []string{"Bug"}}, &changed)
	if changed.Status != "In Progress" || changed.StatusKind != "started" || changed.Assignee == bobName || changed.Due != "" || len(changed.Labels) != 0 || changed.Priority != "high" || changed.Project != "Launch" {
		t.Errorf("after update = %+v", changed)
	}
	claude.fails("update_issue", map[string]any{"ref": made.Ref}, "Nothing to change")

	claude.ok("add_comment", map[string]any{"ref": made.Ref, "body": "Reproduced on **17.4**."}, nil)
	if text := claude.ok("log_time", map[string]any{"ref": made.Ref, "minutes": 45, "note": "repro"}, nil); !strings.Contains(text, "45 minutes") {
		t.Errorf("log_time said %q", text)
	}

	// Reading it back whole.
	second := ada.createIssue(team.Id, "Safari cookie handling").Issue
	ada.expect(http.StatusCreated, "POST", "/api/issues/"+second.Id.String()+"/relations", map[string]any{"id": uuid.New(), "relatedIssueId": stored.Id, "type": "blocks"}, nil)
	var full struct {
		toolIssue
		Description   string
		MinutesLogged int
		Relations     []struct{ Relation, Ref string }
		Comments      []struct{ Author, Body string }
	}
	claude.ok("get_issue", map[string]any{"ref": strings.ToLower(made.Ref)}, &full)
	if !strings.HasPrefix(full.Description, "Steps:") || full.MinutesLogged != 45 || len(full.Comments) != 1 || full.Comments[0].Body != "Reproduced on **17.4**." ||
		len(full.Relations) != 1 || full.Relations[0].Relation != "blocked by" || full.Relations[0].Ref != team.Key+"-2" {
		t.Errorf("get_issue = %+v", full)
	}

	// Listing by fields.
	var list struct {
		Issues []toolIssue
		More   bool
	}
	refs := func() string {
		var out []string
		for _, i := range list.Issues {
			out = append(out, i.Ref)
		}
		return strings.Join(out, ",")
	}
	claude.ok("list_issues", nil, &list)
	if len(list.Issues) != 2 || list.More {
		t.Errorf("open issues = %s", refs())
	}
	claude.ok("list_issues", map[string]any{"assignee": "me", "status": "started"}, &list)
	if refs() != made.Ref {
		t.Errorf("my started issues = %s", refs())
	}
	claude.ok("list_issues", map[string]any{"status": "In Progress", "project": "Launch", "team": team.Key}, &list)
	if refs() != made.Ref {
		t.Errorf("by status name and project = %s", refs())
	}
	claude.ok("list_issues", map[string]any{"assignee": "none"}, &list)
	if refs() != team.Key+"-2" {
		t.Errorf("unassigned = %s", refs())
	}
	claude.ok("list_issues", map[string]any{"status": "closed"}, &list)
	if len(list.Issues) != 0 {
		t.Errorf("closed = %s", refs())
	}
	claude.ok("list_issues", map[string]any{"limit": 1, "sort": "priority"}, &list)
	if refs() != made.Ref || !list.More {
		t.Errorf("first by priority = %s, more %v", refs(), list.More)
	}
	claude.fails("list_issues", map[string]any{"status": "Doing"}, "In Progress")
	claude.fails("list_issues", map[string]any{"cycle": "current"}, "cycle")

	// Searching the text, comments included.
	var found struct {
		Results []struct{ Ref, In, Excerpt string }
	}
	claude.ok("search_issues", map[string]any{"query": "reproduced"}, &found)
	if len(found.Results) != 1 || found.Results[0].Ref != made.Ref || found.Results[0].In != "comment" || strings.Contains(found.Results[0].Excerpt, "<mark>") {
		t.Errorf("search = %+v", found)
	}

	// Projects: progress, health and a forecast.
	if text := claude.ok("post_project_update", map[string]any{"project": "Launch", "health": "at risk", "body": "Safari is holding us up."}, nil); !strings.Contains(text, "atRisk") {
		t.Errorf("post_project_update said %q", text)
	}
	var plan struct {
		Name, Health string
		OpenIssues   int
		Updates      []struct{ Health, Body string }
		Forecast     struct {
			Remaining     int
			EnoughHistory bool
		}
	}
	claude.ok("get_project", map[string]any{"project": "Launch"}, &plan)
	if plan.Health != "atRisk" || plan.OpenIssues != 1 || len(plan.Updates) != 1 || plan.Forecast.Remaining != 1 || plan.Forecast.EnoughHistory {
		t.Errorf("get_project = %+v", plan)
	}
	claude.fails("get_project", map[string]any{"project": "Moonshot"}, "Launch")

	var insights struct {
		Team     string
		Insights map[string]any
	}
	claude.ok("team_insights", nil, &insights)
	if insights.Team != team.Key || insights.Insights["cycleTime"] == nil {
		t.Errorf("team_insights = %+v", insights)
	}

	// Customers' requests, tied to their issue.
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/requests", map[string]any{
		"id": uuid.New(), "title": "Safari support", "requesterEmail": "cust@example.com", "issueId": stored.Id,
	}, nil)
	var requests struct {
		Requests []struct{ Title, Requester, Issue string }
	}
	claude.ok("list_customer_requests", nil, &requests)
	if len(requests.Requests) != 1 || requests.Requests[0].Issue != made.Ref || requests.Requests[0].Requester != "cust@example.com" {
		t.Errorf("requests = %+v", requests)
	}
}

// An assistant can see and do only what its key's owner can.
func TestMCPAccess(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, team := workspaceWithMember(t, env)

	// A private team bob isn't in.
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/teams", map[string]any{"name": "Secret", "key": "SEC", "private": true}, &secret)
	hidden := ada.createIssue(secret.Id, "Acquisition plan").Issue
	open := ada.createIssue(team.Id, "Public work").Issue
	ada.expect(http.StatusCreated, "POST", "/api/issues/"+hidden.Id.String()+"/relations", map[string]any{"id": uuid.New(), "relatedIssueId": open.Id, "type": "blocks"}, nil)

	bobs := env.assistant(t, bob, ws.Id)
	var world struct {
		Teams []struct{ Key string }
	}
	bobs.ok("get_context", nil, &world)
	if len(world.Teams) != 1 || world.Teams[0].Key != team.Key {
		t.Errorf("bob's assistant sees teams %+v", world.Teams)
	}
	var list struct{ Issues []toolIssue }
	bobs.ok("list_issues", map[string]any{"status": "any", "includeArchived": true}, &list)
	if len(list.Issues) != 1 || list.Issues[0].Title != "Public work" {
		t.Errorf("bob's assistant lists %+v", list.Issues)
	}
	bobs.fails("get_issue", map[string]any{"ref": "SEC-1"}, "No issue")
	bobs.fails("update_issue", map[string]any{"ref": "SEC-1", "title": "Mine now"}, "No issue")
	bobs.fails("add_comment", map[string]any{"ref": "SEC-1", "body": "hello"}, "No issue")
	bobs.fails("create_issue", map[string]any{"team": "SEC", "title": "Sneak"}, "No team")
	var found struct{ Results []struct{ Ref string } }
	bobs.ok("search_issues", map[string]any{"query": "acquisition"}, &found)
	if len(found.Results) != 0 {
		t.Errorf("bob's assistant found %+v", found.Results)
	}
	// Nor does the hidden issue show through what it blocks.
	var full struct {
		Relations []struct{ Ref string }
	}
	bobs.ok("get_issue", map[string]any{"ref": team.Key + "-1"}, &full)
	if len(full.Relations) != 0 {
		t.Errorf("relations seen by bob's assistant: %+v", full.Relations)
	}

	// As a guest: no customer requests, and what the API refuses, it refuses.
	ada.createProject(ws.Id, "Launch")
	ada.expect(http.StatusOK, "PATCH", "/api/workspaces/"+ws.Id.String()+"/members/"+bobMe.User.Id.String(), map[string]any{"role": "guest"}, nil)
	bobs.fails("list_customer_requests", nil, "Guests")
	bobs.fails("post_project_update", map[string]any{"project": "Launch", "health": "onTrack"}, "permission")

	// Removed from the workspace, the key stops working at once.
	ada.expect(http.StatusOK, "DELETE", "/api/workspaces/"+ws.Id.String()+"/members/"+bobMe.User.Id.String(), nil, nil)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if res := env.send("POST", "/api/mcp", body, "Authorization", "Bearer "+bobs.key); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("after bob was removed: status %d", res.StatusCode)
	}
}
