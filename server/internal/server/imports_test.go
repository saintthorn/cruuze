package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
)

// runImport starts an import and waits for it to finish.
func (c *client) runImport(workspaceID uuid.UUID, request map[string]any) api.Import {
	c.t.Helper()
	var started api.Import
	c.expect(http.StatusAccepted, "POST", "/api/workspaces/"+workspaceID.String()+"/imports", request, &started)
	c.env.srv.imports.Wait()
	var done api.Import
	c.expect(http.StatusOK, "GET", "/api/imports/"+started.Id.String(), nil, &done)
	return done
}

// lookup finds an issue by KEY-number, archived or not.
func (c *client) lookup(workspaceID uuid.UUID, ref string) api.Issue {
	c.t.Helper()
	var issue api.Issue
	c.expect(http.StatusOK, "GET", "/api/workspaces/"+workspaceID.String()+"/issues/lookup?ref="+ref, nil, &issue)
	return issue
}

// A small tracker: one team with its own statuses, a project, a cycle, and
// issues old and new.
func sampleBundle(now time.Time) map[string]any {
	day := func(n int) string { return now.AddDate(0, 0, n).Format(time.RFC3339) }
	return map[string]any{
		"origin": "test:widgets",
		"label":  "Widgets",
		"users": []map[string]any{
			{"id": "u-ada", "email": "ADA@example.com", "name": "Ada L."},
			{"id": "u-zed", "email": "zed@elsewhere.example", "name": "Zed Zimmer"},
			{"id": "u-bob", "name": "Bobby"},
		},
		"teams": []map[string]any{{
			"id": "t1", "key": "WID", "name": "Widgets",
			"states": []map[string]any{
				{"id": "s-todo", "name": "To Do", "kind": "unstarted"},
				{"id": "s-doing", "name": "Doing", "kind": "started", "color": "f2c94c"},
				{"id": "s-review", "name": "Review", "kind": "started"},
				{"id": "s-done", "name": "Shipped", "kind": "completed"},
			},
		}},
		"labels": []map[string]any{{"id": "l-bug", "name": "Bug", "color": "#ff0000"}, {"id": "l-ui", "name": "UI"}},
		"projects": []map[string]any{{
			"id": "p1", "name": "Launch", "status": "started", "lead": "u-ada", "targetDate": "2027-01-31",
			"milestones": []map[string]any{{"id": "m1", "name": "Beta", "targetDate": "2026-12-01"}},
		}},
		"cycles": []map[string]any{{"id": "c7", "team": "t1", "number": 7, "startsAt": now.AddDate(0, 0, -5).Format(time.DateOnly), "endsAt": now.AddDate(0, 0, 8).Format(time.DateOnly)}},
		"issues": []map[string]any{
			{
				"id": "i-1", "team": "t1", "number": 101, "title": "Old and done", "description": "Shipped long ago.",
				"state": "s-done", "priority": 2, "assignee": "u-ada", "creator": "u-zed", "labels": []string{"l-bug"},
				"createdAt": day(-400), "updatedAt": day(-380), "url": "https://tracker.example/WID-101",
				"history": []map[string]any{
					{"state": "s-todo", "at": day(-400)}, {"state": "s-doing", "at": day(-390)},
					{"state": "s-review", "at": day(-385)}, {"state": "s-done", "at": day(-380)},
				},
				"comments": []map[string]any{
					{"id": "c-1", "author": "u-ada", "body": "On it.", "createdAt": day(-390)},
					{"id": "c-2", "author": "u-zed", "body": "Thanks!", "createdAt": day(-380)},
				},
			},
			{
				"id": "i-2", "team": "t1", "number": 102, "title": "In flight", "state": "s-doing", "priority": 1,
				"assignee": "u-bob", "creator": "u-ada", "labels": []string{"l-ui", "Chore"}, "estimate": 2.6, "dueDate": "2026-12-24",
				"project": "p1", "milestone": "m1", "cycle": "c7", "parent": "i-3",
				"createdAt": day(-10), "updatedAt": day(-1), "startedAt": day(-3),
				"relations": []map[string]any{{"type": "blocked_by", "issue": "i-3"}, {"type": "related", "issue": "i-1"}},
			},
			{
				"id": "i-3", "team": "t1", "number": 105, "title": "The parent", "state": "s-todo", "createdAt": day(-20), "updatedAt": day(-20),
				"relations": []map[string]any{{"type": "blocks", "issue": "i-2"}, {"type": "related", "issue": "i-1"}},
			},
			{"id": "i-4", "team": "t1", "number": 106, "title": "Done last week", "state": "s-done", "createdAt": day(-30), "updatedAt": day(-7), "completedAt": day(-7)},
		},
	}
}

func TestImportBundle(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, bobMe, ws, team := workspaceWithMember(t, env)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	importsPath := "/api/workspaces/" + ws.Id.String() + "/imports"
	// A label that is already here is used, not duplicated.
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/labels", map[string]any{"id": uuid.New(), "name": "bug", "color": "#eb5757"}, nil)

	request := map[string]any{"source": "bundle", "bundle": sampleBundle(now), "userMap": map[string]string{"u-bob": bobMe.User.Id.String()}}

	// Only admins import.
	bob.expect(http.StatusForbidden, "POST", importsPath, request, nil)
	bob.expect(http.StatusForbidden, "POST", importsPath+"/preview", request, nil)
	ada.expect(http.StatusBadRequest, "POST", importsPath, map[string]any{"source": "bundle"}, nil)
	ada.expect(http.StatusBadRequest, "POST", importsPath, map[string]any{"source": "bundle", "bundle": map[string]any{"teams": []any{}}}, nil)
	ada.expect(http.StatusBadRequest, "POST", importsPath, map[string]any{"source": "trello"}, nil)

	// The preview says what is there and who matches.
	var preview api.ImportPreview
	ada.expect(http.StatusOK, "POST", importsPath+"/preview", request, &preview)
	if preview.Label != "Widgets" || len(preview.Teams) != 1 || preview.Teams[0].Issues != 4 || preview.Teams[0].TeamId != nil || len(preview.Users) != 3 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Users[0].UserId == nil || *preview.Users[0].UserId != adaMe.User.Id || preview.Users[1].UserId != nil {
		t.Errorf("preview users = %+v", preview.Users)
	}

	cursor := ada.lastSyncID(ws.Id)
	done := ada.runImport(ws.Id, request)
	if done.Status != api.ImportStatusDone || done.Error != "" || done.Label != "Widgets" || done.FinishedAt == nil {
		t.Fatalf("import = %+v", done)
	}
	want := map[string]int{"teams": 1, "statuses": 5, "labels": 2, "projects": 1, "milestones": 1, "cycles": 1, "issues": 4, "comments": 2}
	for key, n := range want {
		if done.Counts[key] != n {
			t.Errorf("counts[%s] = %d, want %d (all: %v)", key, done.Counts[key], n, done.Counts)
		}
	}
	if len(done.Warnings) != 0 {
		t.Errorf("warnings = %v", done.Warnings)
	}

	// Everyone's copy starts over rather than being patched.
	if batch := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, cursor); !batch.Reset {
		t.Errorf("clients were not told to start over: %+v", batch)
	}

	boot := ada.bootstrap(ws.Id)
	var wid api.TeamRecord
	for _, candidate := range boot.Teams {
		if candidate.Key == "WID" {
			wid = candidate
		}
	}
	if wid.Name != "Widgets" {
		t.Fatalf("teams = %+v", boot.Teams)
	}
	states := map[string]api.WorkflowState{}
	for _, s := range boot.WorkflowStates {
		if s.TeamId == wid.Id {
			states[s.Name] = s
		}
	}
	// The source's four, plus a canceled status it lacked.
	if len(states) != 5 || states["Doing"].Type != api.WorkflowStateTypeStarted || states["Doing"].Color != "#f2c94c" || states["Canceled"].Type != api.WorkflowStateTypeCanceled {
		t.Errorf("statuses = %+v", states)
	}
	if len(boot.Labels) != 3 {
		t.Errorf("labels = %+v, want bug (kept), UI and Chore", boot.Labels)
	}

	// Numbers, dates, people and plan survive.
	flight := ada.lookup(ws.Id, "WID-102")
	parent := ada.lookup(ws.Id, "WID-105")
	if flight.Title != "In flight" || flight.StateId != states["Doing"].Id || flight.Priority != 1 ||
		flight.AssigneeId == nil || *flight.AssigneeId != bobMe.User.Id || flight.CreatorId == nil || *flight.CreatorId != adaMe.User.Id ||
		flight.DueDate == nil || flight.DueDate.Format(time.DateOnly) != "2026-12-24" ||
		len(flight.LabelIds) != 2 || flight.ProjectId == nil || flight.MilestoneId == nil || flight.CycleId == nil ||
		flight.ParentId == nil || *flight.ParentId != parent.Id {
		t.Errorf("WID-102 = %+v", flight)
	}
	if !flight.CreatedAt.Equal(now.AddDate(0, 0, -10)) || !flight.UpdatedAt.Equal(now.AddDate(0, 0, -1)) || flight.StartedAt == nil || !flight.StartedAt.Equal(now.AddDate(0, 0, -3)) || flight.CompletedAt != nil {
		t.Errorf("WID-102 dates: created %v updated %v started %v completed %v", flight.CreatedAt, flight.UpdatedAt, flight.StartedAt, flight.CompletedAt)
	}
	// Old closed work goes straight to the archive; recent closed work stays.
	old := ada.lookup(ws.Id, "WID-101")
	if old.ArchivedAt == nil || old.CompletedAt == nil || !old.CompletedAt.Equal(now.AddDate(0, 0, -380)) || old.StartedAt == nil || !old.StartedAt.Equal(now.AddDate(0, 0, -390)) {
		t.Errorf("WID-101: archived %v completed %v started %v", old.ArchivedAt, old.CompletedAt, old.StartedAt)
	}
	if recent := ada.lookup(ws.Id, "WID-106"); recent.ArchivedAt != nil || recent.CompletedAt == nil || !recent.CompletedAt.Equal(now.AddDate(0, 0, -7)) {
		t.Errorf("WID-106: archived %v completed %v", recent.ArchivedAt, recent.CompletedAt)
	}
	// Someone who isn't here is credited in the text; the link back is kept.
	if !strings.Contains(old.Description, "Shipped long ago.") || !strings.Contains(old.Description, "[tracker.example](https://tracker.example/WID-101)") || !strings.Contains(old.Description, "Opened by Zed Zimmer") || old.CreatorId != nil {
		t.Errorf("WID-101 description = %q, creator %v", old.Description, old.CreatorId)
	}
	details := ada.details("/api/issues/" + old.Id.String())
	if len(details.Comments) != 2 || details.Comments[0].Body != "On it." || details.Comments[0].UserId == nil ||
		details.Comments[1].UserId != nil || !strings.HasPrefix(details.Comments[1].Body, "**Zed Zimmer** wrote:") || !details.Comments[1].CreatedAt.Equal(now.AddDate(0, 0, -380)) {
		t.Errorf("comments = %+v", details.Comments)
	}
	// Time in progress comes from the imported history: ten days, long ago.
	if got := details.InProgressSeconds; got != 10*24*3600 {
		t.Errorf("WID-101 in progress = %d s, want ten days", got)
	}
	var history []string
	rows, err := env.pool.Query(ctx, `SELECT state_type || ':' || (left_at IS NULL) FROM issue_state_history WHERE issue_id = $1 ORDER BY entered_at`, old.Id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatal(err)
		}
		history = append(history, entry)
	}
	rows.Close()
	if got := strings.Join(history, " "); got != "unstarted:false started:false started:false completed:true" {
		t.Errorf("WID-101 history = %s", got)
	}

	// Relations, each once, whichever side named them.
	var relations []string
	names := map[uuid.UUID]string{flight.Id: "102", parent.Id: "105", old.Id: "101"}
	relRows, err := env.pool.Query(ctx, `SELECT issue_id, related_issue_id, type FROM issue_relations WHERE workspace_id = $1`, ws.Id)
	if err != nil {
		t.Fatal(err)
	}
	for relRows.Next() {
		var from, to uuid.UUID
		var kind string
		if err := relRows.Scan(&from, &to, &kind); err != nil {
			t.Fatal(err)
		}
		pair := []string{names[from], names[to]}
		if kind == "related" {
			slices.Sort(pair)
		}
		relations = append(relations, fmt.Sprintf("%s %s %s", pair[0], kind, pair[1]))
	}
	relRows.Close()
	slices.Sort(relations)
	if got := strings.Join(relations, "; "); got != "101 related 102; 101 related 105; 105 blocks 102" {
		t.Errorf("relations = %s", got)
	}
	// The one between live issues reaches clients (the others touch an archived issue).
	if len(boot.Relations) != 1 || boot.Relations[0].IssueId != parent.Id || boot.Relations[0].RelatedIssueId != flight.Id {
		t.Errorf("relations in the bootstrap = %+v", boot.Relations)
	}
	// New issues are numbered after the highest imported one.
	if next := ada.createIssue(wid.Id, "Made here").Issue; next.Number != 107 {
		t.Errorf("next number in WID = %d, want 107", next.Number)
	}
	// The team's flow numbers work on day one.
	var insights api.TeamInsights
	ada.expect(http.StatusOK, "GET", "/api/teams/"+wid.Id.String()+"/insights", nil, &insights)
	raw, _ := json.Marshal(insights)
	if !strings.Contains(string(raw), `"completed"`) {
		t.Errorf("insights = %s", raw)
	}

	// Again, with one change at the source: nothing is duplicated.
	bundle := sampleBundle(now)
	issues := bundle["issues"].([]map[string]any)
	issues[1]["title"] = "In flight, renamed"
	issues[1]["updatedAt"] = now.Format(time.RFC3339)
	issues[1]["state"] = "s-review"
	issues[1]["comments"] = []map[string]any{{"id": "c-3", "author": "u-ada", "body": "Nearly there.", "createdAt": now.Format(time.RFC3339)}}
	again := ada.runImport(ws.Id, map[string]any{"source": "bundle", "bundle": bundle, "userMap": map[string]string{"u-bob": bobMe.User.Id.String()}})
	if again.Status != api.ImportStatusDone || again.Counts["issues"] != 0 || again.Counts["updated"] != 1 || again.Counts["unchanged"] != 3 || again.Counts["comments"] != 1 || again.Counts["teams"] != 0 || again.Counts["labels"] != 0 {
		t.Errorf("second import = %+v", again)
	}
	renamed := ada.lookup(ws.Id, "WID-102")
	if renamed.Id != flight.Id || renamed.Title != "In flight, renamed" || renamed.StateId != states["Review"].Id || renamed.ParentId == nil {
		t.Errorf("after the second import, WID-102 = %+v", renamed)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM issues i JOIN teams t ON t.id = i.team_id WHERE t.key = 'WID'`).Scan(&count); err != nil || count != 5 {
		t.Errorf("%d issues in WID after importing twice, want 5 (%v)", count, err)
	}

	// Into a team that has issues of its own: new numbers, and it says so.
	ada.createIssue(team.Id, "Already here")
	second := sampleBundle(now)
	second["origin"] = "test:other"
	second["teams"].([]map[string]any)[0]["id"] = "t9"
	for _, issue := range second["issues"].([]map[string]any) {
		issue["team"] = "t9"
	}
	second["cycles"] = nil
	into := ada.runImport(ws.Id, map[string]any{"source": "bundle", "bundle": second, "teamMap": map[string]any{"t9": team.Id}})
	if into.Status != api.ImportStatusDone || into.Counts["issues"] != 4 || into.Counts["teams"] != 0 || len(into.Warnings) != 1 || !strings.Contains(into.Warnings[0], "new numbers") {
		t.Errorf("import into an existing team = %+v", into)
	}
	if moved := ada.lookup(ws.Id, team.Key+"-2"); moved.Title != "Old and done" {
		t.Errorf("%s-2 = %q", team.Key, moved.Title)
	}

	// The history is listed; undoing takes away what an import made.
	var list []api.Import
	ada.expect(http.StatusOK, "GET", importsPath, nil, &list)
	if len(list) != 3 || list[0].Id != into.Id {
		t.Errorf("history has %d imports", len(list))
	}
	bob.expect(http.StatusForbidden, "POST", "/api/imports/"+done.Id.String()+"/undo", nil, nil)
	var undone api.Import
	ada.expect(http.StatusOK, "POST", "/api/imports/"+into.Id.String()+"/undo", nil, &undone)
	if undone.Status != api.ImportStatusUndone {
		t.Errorf("undone = %+v", undone)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE team_id = $1`, team.Id).Scan(&count); err != nil || count != 1 {
		t.Errorf("%d issues left in the team after the undo, want its own 1 (%v)", count, err)
	}
	// Undoing the first import removes the team it made, but not the issue
	// someone created there since, nor the label that was here before.
	ada.expect(http.StatusOK, "POST", "/api/imports/"+done.Id.String()+"/undo", nil, nil)
	after := ada.bootstrap(ws.Id)
	if len(after.Issues) != 2 {
		t.Errorf("%d issues after undoing everything, want the two made by hand", len(after.Issues))
	}
	var labels []string
	for _, l := range after.Labels {
		labels = append(labels, l.Name)
	}
	if len(after.Labels) != 1 || after.Labels[0].Name != "bug" {
		t.Errorf("labels after the undo = %v, want only the one that was here before", labels)
	}
	if len(after.Projects) != 0 || len(after.Cycles) != 0 {
		t.Errorf("after the undo: %d projects, %d cycles", len(after.Projects), len(after.Cycles))
	}
	// WID still holds an issue made by hand, so it stays, with the status that issue is in.
	if _, err := env.q.GetIssueByTeamKeyAndNumber(ctx, getIssueParams(ws.Id, "WID", 107)); err != nil {
		t.Errorf("the issue made by hand in WID is gone: %v", err)
	}
}

func getIssueParams(workspaceID uuid.UUID, key string, number int32) dbgen.GetIssueByTeamKeyAndNumberParams {
	return dbgen.GetIssueByTeamKeyAndNumberParams{WorkspaceID: workspaceID, Key: key, Number: number}
}

// canned answers for importers' HTTP calls, by "METHOD path?query".
type cannedAPI map[string]string

func (c cannedAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.Path
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}
	status, body := http.StatusOK, c[key]
	if body == "" {
		status, body = http.StatusNotFound, `{"message":"Not Found"}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// sinceAPI answers as the API behind it does, after noting and removing the
// "since" of requests that ask only for what changed.
type sinceAPI struct {
	api  http.RoundTripper
	mu   sync.Mutex
	seen []string
}

func (s *sinceAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	query := req.URL.Query()
	if since := query.Get("since"); since != "" {
		s.mu.Lock()
		s.seen = append(s.seen, since)
		s.mu.Unlock()
		query.Del("since")
		req = req.Clone(req.Context())
		req.URL.RawQuery = query.Encode()
	}
	return s.api.RoundTrip(req)
}

func (s *sinceAPI) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := s.seen
	s.seen = nil
	return seen
}

func TestCoversTeams(t *testing.T) {
	some, none := []string{"A", "B"}, []string{}
	cases := []struct {
		earlier string
		wanted  *[]string
		want    bool
	}{
		{`{}`, nil, true},                        // everything before, everything now
		{`{}`, &some, true},                      // everything before covers any choice
		{`{"teams":["A","B","C"]}`, &some, true}, // a narrower choice
		{`{"teams":["A"]}`, &some, false},        // B was never read
		{`{"teams":["A"]}`, nil, false},          // everything now, only A before
		{`{"teams":["A"]}`, &none, false},        // an empty choice is everything
		{`not json`, &some, false},
	}
	for _, c := range cases {
		if got := coversTeams([]byte(c.earlier), c.wanted); got != c.want {
			t.Errorf("coversTeams(%s, %v) = %v, want %v", c.earlier, c.wanted, got, c.want)
		}
	}
}

func TestImportGitHub(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, _ := workspaceWithMember(t, env)
	repo := "/repos/acme/widgets"
	github := &sinceAPI{}
	env.srv.importHTTP = &http.Client{Transport: github}
	github.api = cannedAPI{
		"GET " + repo: `{"full_name":"acme/widgets","has_issues":true}`,
		"GET /search/issues?per_page=1&q=repo%3Aacme%2Fwidgets+is%3Aissue": `{"total_count":2}`,
		"GET " + repo + "/assignees?per_page=100":                          `[{"login":"Bob-Dev"},{"login":"octocat"}]`,
		"GET " + repo + "/labels?per_page=100":                             `[{"name":"bug","color":"d73a4a"}]`,
		"GET " + repo + "/milestones?per_page=100&state=all":               `[{"number":1,"title":"v1.0","state":"open","due_on":"2027-03-01T08:00:00Z"}]`,
		"GET " + repo + "/issues?direction=asc&per_page=100&sort=created&state=all": `[
			{"number":7,"title":"Crash on start","body":"It crashes.","state":"closed","state_reason":"completed",
			 "user":{"login":"octocat"},"assignee":{"login":"Bob-Dev"},"labels":[{"name":"bug"}],"milestone":{"number":1},
			 "created_at":"2026-09-01T09:00:00Z","updated_at":"2026-09-20T10:00:00Z","closed_at":"2026-09-20T10:00:00Z",
			 "html_url":"https://github.com/acme/widgets/issues/7"},
			{"number":8,"title":"A pull request","state":"open","user":{"login":"octocat"},"labels":[],
			 "created_at":"2026-09-02T09:00:00Z","updated_at":"2026-09-02T09:00:00Z","pull_request":{}},
			{"number":9,"title":"Dark mode","body":"Please.","state":"open","user":{"login":"Bob-Dev"},"labels":[],
			 "created_at":"2026-09-03T09:00:00Z","updated_at":"2026-09-03T09:00:00Z","html_url":"https://github.com/acme/widgets/issues/9"}]`,
		"GET " + repo + "/issues/comments?direction=asc&per_page=100&sort=created": `[
			{"id":1,"body":"Fixed.","user":{"login":"Bob-Dev"},"issue_url":"https://api.github.com/repos/acme/widgets/issues/7","created_at":"2026-09-20T09:59:00Z"},
			{"id":2,"body":"On the pull request.","user":{"login":"octocat"},"issue_url":"https://api.github.com/repos/acme/widgets/issues/8","created_at":"2026-09-02T10:00:00Z"}]`,
	}
	// Bob has said who he is on GitHub, which is how GitHub people are matched.
	bob.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"githubLogin": "bob-dev"}, nil)
	request := map[string]any{"source": "github", "github": map[string]any{"repo": "acme/widgets", "token": "ghp_x"}}
	importsPath := "/api/workspaces/" + ws.Id.String() + "/imports"

	var preview api.ImportPreview
	ada.expect(http.StatusOK, "POST", importsPath+"/preview", request, &preview)
	if preview.Label != "acme/widgets" || preview.Teams[0].Key != "WIDGETS" || preview.Teams[0].Issues != 2 ||
		len(preview.Users) != 2 || preview.Users[0].UserId == nil || *preview.Users[0].UserId != bobMe.User.Id || preview.Users[1].UserId != nil {
		t.Fatalf("preview = %+v", preview)
	}
	ada.expect(http.StatusBadRequest, "POST", importsPath+"/preview", map[string]any{"source": "github", "github": map[string]any{"repo": "nope"}}, nil)
	ada.expect(http.StatusBadRequest, "POST", importsPath, map[string]any{"source": "github", "github": map[string]any{"repo": "acme/missing"}}, nil)

	done := ada.runImport(ws.Id, request)
	if done.Status != api.ImportStatusDone || done.Source != "github" || done.Label != "acme/widgets" ||
		done.Counts["issues"] != 2 || done.Counts["comments"] != 1 || done.Counts["projects"] != 1 || done.Counts["teams"] != 1 {
		t.Fatalf("import = %+v", done)
	}
	crash := ada.lookup(ws.Id, "WIDGETS-7")
	if crash.Title != "Crash on start" || crash.AssigneeId == nil || *crash.AssigneeId != bobMe.User.Id || crash.CreatorId != nil ||
		crash.ProjectId == nil || crash.CompletedAt == nil || !strings.Contains(crash.Description, "Opened by octocat") {
		t.Errorf("WIDGETS-7 = %+v", crash)
	}
	if dark := ada.lookup(ws.Id, "WIDGETS-9"); dark.CreatorId == nil || *dark.CreatorId != bobMe.User.Id || dark.CompletedAt != nil {
		t.Errorf("WIDGETS-9 = %+v", dark)
	}
	// The pull request is not an issue here, and its comment went nowhere.
	ada.expect(http.StatusNotFound, "GET", "/api/workspaces/"+ws.Id.String()+"/issues/lookup?ref=WIDGETS-8", nil, nil)
	if comments := ada.details("/api/issues/" + crash.Id.String()).Comments; len(comments) != 1 || comments[0].Body != "Fixed." || comments[0].UserId == nil {
		t.Errorf("comments = %+v", comments)
	}
	// The team got an in-progress status, which GitHub has no word for.
	kinds := map[api.WorkflowStateType]int{}
	for _, s := range ada.bootstrap(ws.Id).WorkflowStates {
		if s.TeamId == crash.TeamId {
			kinds[s.Type]++
		}
	}
	if kinds[api.WorkflowStateTypeStarted] != 1 || kinds[api.WorkflowStateTypeUnstarted] != 1 || kinds[api.WorkflowStateTypeCompleted] != 1 || kinds[api.WorkflowStateTypeCanceled] != 1 {
		t.Errorf("status kinds = %v", kinds)
	}
	if since := github.take(); len(since) != 0 {
		t.Errorf("the first import asked only for changes since %v", since)
	}
	// Running it again changes nothing, and asks GitHub only for what
	// changed since the first run began.
	if again := ada.runImport(ws.Id, request); again.Counts["issues"] != 0 || again.Counts["unchanged"] != 2 || again.Counts["comments"] != 0 {
		t.Errorf("second run = %+v", again)
	}
	since := github.take()
	if len(since) != 2 {
		t.Fatalf("second run asked for changes %d times, want for issues and for comments: %v", len(since), since)
	}
	if at, err := time.Parse(time.RFC3339, since[0]); err != nil || !at.Before(done.CreatedAt) || done.CreatedAt.Sub(at) > time.Hour {
		t.Errorf("second run read since %q, want shortly before the first began (%s)", since[0], done.CreatedAt)
	}
	// Unless everything is asked for.
	request["full"] = true
	if full := ada.runImport(ws.Id, request); full.Status != api.ImportStatusDone || full.Counts["unchanged"] != 2 {
		t.Errorf("full run = %+v", full)
	}
	if since := github.take(); len(since) != 0 {
		t.Errorf("a full import asked only for changes since %v", since)
	}
}

func TestImportJiraWithFiles(t *testing.T) {
	env := newTestEnv(t)
	ada, _, adaMe, _, ws, _ := workspaceWithMember(t, env)
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01"
	fields := "summary,description,status,priority,assignee,reporter,labels,components,duedate,created,updated,resolutiondate,resolution,issuetype,parent,issuelinks,comment,attachment"
	search := func(updated string) string {
		return `{"startAt":0,"total":1,"issues":[{"key":"PLAT-7","fields":{
			"summary":"Tune the cache","description":"See !mock.png|thumbnail! and [the log|https://jira.acme.example/secure/attachment/901/log.txt].",
			"status":{"id":"3","name":"In Progress","statusCategory":{"key":"indeterminate"}},"priority":{"name":"Major"},"issuetype":{"name":"Bug"},
			"assignee":{"key":"ada","displayName":"Ada","emailAddress":"ada@example.com"},"reporter":{"key":"zed","displayName":"Zed"},
			"created":"2026-08-01T10:00:00.000+0000","updated":"` + updated + `",
			"attachment":[
				{"id":"900","filename":"mock.png","content":"https://jira.acme.example/secure/attachment/900/mock.png"},
				{"id":"901","filename":"log.txt","content":"https://jira.acme.example/secure/attachment/901/log.txt"},
				{"id":"902","filename":"gone.bin","content":"https://jira.acme.example/secure/attachment/902/gone.bin"}],
			"comment":{"total":1,"comments":[{"id":"70","author":{"key":"zed","displayName":"Zed"},"body":"Same as !mock.png!","created":"2026-08-02T10:00:00.000+0000"}]}},
			"changelog":{"total":1,"histories":[{"created":"2026-08-01T12:00:00.000+0000","items":[{"field":"status","from":"1","to":"3"}]}]}}]}`
	}
	query := "?expand=changelog&fields=" + strings.ReplaceAll(fields, ",", "%2C") + "&jql=project+%3D+%22PLAT%22+ORDER+BY+created+ASC&maxResults=100&startAt=0"
	api2 := "/rest/api/2"
	canned := cannedAPI{
		"GET " + api2 + "/serverInfo":            `{"deploymentType":"Server","serverTitle":"Acme Jira"}`,
		"GET " + api2 + "/myself":                `{"key":"ada"}`,
		"GET " + api2 + "/field":                 `[]`,
		"GET " + api2 + "/project":               `[{"id":"1","key":"PLAT","name":"Platform"}]`,
		"GET " + api2 + "/project/PLAT/statuses": `[{"name":"Bug","statuses":[{"id":"1","name":"Open","statusCategory":{"key":"new"}},{"id":"3","name":"In Progress","statusCategory":{"key":"indeterminate"}},{"id":"6","name":"Closed","statusCategory":{"key":"done"}}]}]`,
		"GET " + api2 + "/search" + query:        search("2026-08-02T10:00:00.000+0000"),
		"GET /secure/attachment/900/mock.png":    png,
		"GET /secure/attachment/901/log.txt":     "line one\nline two\n",
	}
	env.srv.importHTTP = &http.Client{Transport: canned}
	request := map[string]any{"source": "jira", "jira": map[string]any{"baseUrl": "https://jira.acme.example", "token": "pat"}}

	done := ada.runImport(ws.Id, request)
	if done.Status != api.ImportStatusDone || done.Label != "Acme Jira" || done.Counts["issues"] != 1 || done.Counts["files"] != 2 || done.Counts["comments"] != 1 {
		t.Fatalf("import = %+v", done)
	}
	// The file that couldn't be fetched is mentioned, and didn't stop anything.
	if len(done.Warnings) != 1 || !strings.Contains(done.Warnings[0], "couldn't be copied") {
		t.Errorf("warnings = %v", done.Warnings)
	}
	issue := ada.lookup(ws.Id, "PLAT-7")
	if issue.AssigneeId == nil || *issue.AssigneeId != adaMe.User.Id || issue.Priority != 2 || issue.StartedAt == nil {
		t.Errorf("PLAT-7 = %+v", issue)
	}
	var files []api.Attachment
	for _, a := range ada.bootstrap(ws.Id).Attachments {
		if a.IssueId == issue.Id {
			files = append(files, a)
		}
	}
	if len(files) != 2 {
		t.Fatalf("attachments = %+v", files)
	}
	byName := map[string]api.Attachment{}
	for _, f := range files {
		byName[f.Filename] = f
	}
	image, log := byName["mock.png"], byName["log.txt"]
	if image.ContentType != "image/png" || !strings.HasPrefix(log.ContentType, "text/plain") {
		t.Errorf("types: %q, %q", image.ContentType, log.ContentType)
	}
	// The text points at the copies: the image embedded, the log linked.
	wantText := "See ![mock.png](/api/attachments/" + image.Id.String() + ") and [the log](/api/attachments/" + log.Id.String() + ")."
	if !strings.HasPrefix(issue.Description, wantText) {
		t.Errorf("description = %q\nwant prefix  %q", issue.Description, wantText)
	}
	comments := ada.details("/api/issues/" + issue.Id.String()).Comments
	if len(comments) != 1 || !strings.HasSuffix(comments[0].Body, "Same as ![mock.png](/api/attachments/"+image.Id.String()+")") {
		t.Errorf("comment = %+v", comments)
	}
	// And the bytes are served like any other attachment.
	res := ada.raw("GET", "/api/attachments/"+image.Id.String(), nil, "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != png {
		t.Errorf("served attachment: status %d, %d bytes", res.StatusCode, len(body))
	}

	// When the issue changes at the source, its text is taken again and
	// pointed at the same copies: nothing is downloaded twice.
	delete(canned, "GET /secure/attachment/900/mock.png")
	delete(canned, "GET /secure/attachment/901/log.txt")
	canned["GET "+api2+"/search"+query] = search("2026-09-01T10:00:00.000+0000")
	request["full"] = true // the canned search is the one for everything
	again := ada.runImport(ws.Id, request)
	if again.Status != api.ImportStatusDone || again.Counts["updated"] != 1 || again.Counts["files"] != 0 {
		t.Fatalf("second import = %+v", again)
	}
	if after := ada.lookup(ws.Id, "PLAT-7"); !strings.HasPrefix(after.Description, wantText) {
		t.Errorf("description after the second import = %q", after.Description)
	}

	// Undoing the first import takes the files with it.
	ada.expect(http.StatusOK, "POST", "/api/imports/"+done.Id.String()+"/undo", nil, nil)
	if res := ada.raw("GET", "/api/attachments/"+image.Id.String(), nil, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("the attachment after the undo: status %d", res.StatusCode)
	}
	if len(ada.bootstrap(ws.Id).Attachments) != 0 {
		t.Error("attachments remain after the undo")
	}
}

func TestImportCSV(t *testing.T) {
	env := newTestEnv(t)
	ada, _, adaMe, _, ws, _ := workspaceWithMember(t, env)
	text := "ID,Team,Title,Status,Assignee,Created,Completed,Parent issue\n" +
		"SUP-1,Support,Refund not received,Done,ADA@example.com,2026-09-20T10:00:00Z,2026-09-25T10:00:00Z,\n" +
		"SUP-2,Support,Cannot reset password,In Progress,,2026-09-28T10:00:00Z,,SUP-1\n" +
		"OPS-5,Operations,Rotate keys,Todo,,2026-09-29T10:00:00Z,,\n"
	request := map[string]any{"source": "csv", "csv": map[string]any{"text": text, "name": "export"}}
	importsPath := "/api/workspaces/" + ws.Id.String() + "/imports"

	var preview api.ImportPreview
	ada.expect(http.StatusOK, "POST", importsPath+"/preview", request, &preview)
	if preview.Label != "export (CSV)" || len(preview.Teams) != 2 || preview.Teams[0].Key != "SUP" || preview.Teams[0].Issues != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	ada.expect(http.StatusBadRequest, "POST", importsPath+"/preview", map[string]any{"source": "csv", "csv": map[string]any{"text": "Status\nDone\n"}}, nil)

	// Only the team that was chosen.
	request["teams"] = []string{"SUP"}
	done := ada.runImport(ws.Id, request)
	if done.Status != api.ImportStatusDone || done.Counts["issues"] != 2 || done.Counts["teams"] != 1 {
		t.Fatalf("import = %+v", done)
	}
	refund, reset := ada.lookup(ws.Id, "SUP-1"), ada.lookup(ws.Id, "SUP-2")
	if refund.AssigneeId == nil || *refund.AssigneeId != adaMe.User.Id || refund.CompletedAt == nil || !refund.CreatedAt.Equal(time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("SUP-1 = %+v", refund)
	}
	if reset.ParentId == nil || *reset.ParentId != refund.Id || reset.StartedAt == nil {
		t.Errorf("SUP-2 = %+v", reset)
	}
	ada.expect(http.StatusNotFound, "GET", "/api/workspaces/"+ws.Id.String()+"/issues/lookup?ref=OPS-5", nil, nil)
}

func TestImportAsProjectsAndSelf(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, _, ws, team := workspaceWithMember(t, env)
	ctx := context.Background()
	importsPath := "/api/workspaces/" + ws.Id.String() + "/imports"
	bob.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"name": "Bob Builder"}, nil)

	// A tracker that gives no email addresses: one person is whoever the
	// credentials belong to, one shares a member's name, one is a stranger.
	bundle := map[string]any{
		"origin": "test:wellness",
		"label":  "wellness.example",
		"users": []map[string]any{
			{"id": "u-me", "name": "A. Lovelace (work)", "self": true},
			{"id": "u-bob", "name": "bob  builder"},
			{"id": "u-x", "name": "Somebody Else"},
		},
		"teams": []map[string]any{{"id": "t1", "key": "WEL", "name": "Wellness"}},
		"issues": []map[string]any{
			{"id": "i-1", "team": "t1", "number": 5, "title": "Mine", "assignee": "u-me"},
			{"id": "i-2", "team": "t1", "number": 6, "title": "Bob's", "assignee": "u-bob"},
			{"id": "i-3", "team": "t1", "number": 7, "title": "A stranger's", "assignee": "u-x"},
		},
	}

	// The wizard is shown the matches the import will make.
	var preview api.ImportPreview
	ada.expect(http.StatusOK, "POST", importsPath+"/preview", map[string]any{"source": "bundle", "bundle": bundle}, &preview)
	matched := map[string]bool{}
	for _, u := range preview.Users {
		matched[u.Id] = u.UserId != nil
		if u.Id == "u-me" && (u.UserId == nil || *u.UserId != adaMe.User.Id) {
			t.Errorf("preview: the credentials' owner matched %v, want the person importing", u.UserId)
		}
	}
	if !matched["u-me"] || !matched["u-bob"] || matched["u-x"] {
		t.Errorf("preview matches = %v, want u-me and u-bob but not u-x", matched)
	}

	// As projects, every source team needs a team to become a project of.
	orphan := ada.runImport(ws.Id, map[string]any{"source": "bundle", "bundle": bundle, "asProjects": true})
	if orphan.Status != api.ImportStatusFailed || !strings.Contains(orphan.Error, "becomes a project of") {
		t.Fatalf("as projects without a team: %+v", orphan)
	}

	done := ada.runImport(ws.Id, map[string]any{
		"source": "bundle", "bundle": bundle, "asProjects": true, "teamMap": map[string]string{"t1": team.Id.String()},
	})
	if done.Status != api.ImportStatusDone || done.Counts["issues"] != 3 || done.Counts["projects"] != 1 || done.Counts["teams"] != 0 {
		t.Fatalf("import = %+v", done)
	}
	teams, err := env.q.ListTeamsOfWorkspace(ctx, ws.Id)
	if err != nil || len(teams) != 1 {
		t.Fatalf("teams after the import = %d (%v), want the one there was", len(teams), err)
	}

	mine := ada.lookup(ws.Id, team.Key+"-5")
	bobs := ada.lookup(ws.Id, team.Key+"-6")
	strangers := ada.lookup(ws.Id, team.Key+"-7")
	if mine.AssigneeId == nil || *mine.AssigneeId != adaMe.User.Id {
		t.Errorf("the importer's own issue is assigned to %v", mine.AssigneeId)
	}
	if bobs.AssigneeId == nil || strangers.AssigneeId != nil {
		t.Errorf("assignees: bob's %v, the stranger's %v", bobs.AssigneeId, strangers.AssigneeId)
	}
	if mine.ProjectId == nil || bobs.ProjectId == nil || *mine.ProjectId != *bobs.ProjectId || mine.TeamId != team.Id {
		t.Errorf("issues aren't together in one project of the chosen team: %+v / %+v", mine.ProjectId, bobs.ProjectId)
	}

	// Running it again makes nothing new, and undoing takes the project with it.
	again := ada.runImport(ws.Id, map[string]any{
		"source": "bundle", "bundle": bundle, "asProjects": true, "teamMap": map[string]string{"t1": team.Id.String()},
	})
	if again.Status != api.ImportStatusDone || again.Counts["projects"] != 0 || again.Counts["issues"] != 0 {
		t.Errorf("second run = %+v, want nothing new", again.Counts)
	}
}
