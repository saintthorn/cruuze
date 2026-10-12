package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/syncer"
)

// send makes a request with an arbitrary body and headers, without cookies.
func (e *testEnv) send(method, path string, body []byte, headers ...string) *http.Response {
	req := httptest.NewRequest(method, "http://app.test"+path, bytes.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Result()
}

func TestSearchQuery(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"  ":                     "",
		"login":                  "login:*",
		"Login  bug":             "Login:* & bug:*",
		"ENG-12":                 "ENG:* & 12:*",
		"a & !b | (c) <-> 'd':*": "a:* & b:* & c:* & d:*",
		"naïve café":             "naïve:* & café:*",
	}
	for in, want := range cases {
		if got := searchQuery(in); got != want {
			t.Errorf("searchQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()

	private := true
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)

	issue := func(teamID uuid.UUID, title, description string) api.Issue {
		var out api.IssueMutationResult
		ada.expect(http.StatusCreated, "POST", "/api/teams/"+teamID.String()+"/issues",
			api.CreateIssueRequest{Id: newID(), Title: title, Description: &description}, &out)
		return out.Issue
	}
	login := issue(team.Id, "Login page crashes", "Happens after the password reset email.")
	billing := issue(team.Id, "Invoice totals are wrong", "Rounding of the tax line.")
	hidden := issue(secret.Id, "Login audit for the secret project", "")
	ada.expect(http.StatusCreated, "POST", "/api/issues/"+billing.Id.String()+"/comments",
		api.CreateCommentRequest{Id: newID(), Body: "Customers see this on the login screen too."}, nil)

	search := func(c *client, q string) []api.SearchResult {
		t.Helper()
		var out api.SearchResults
		c.expect(http.StatusOK, "GET", base+"/search?q="+strings.ReplaceAll(q, " ", "+"), nil, &out)
		return out.Results
	}
	ids := func(results []api.SearchResult) []uuid.UUID {
		out := make([]uuid.UUID, len(results))
		for i, r := range results {
			out[i] = r.IssueId
		}
		return out
	}

	// Title matches rank above comment matches; private teams stay hidden.
	got := search(bob, "login")
	if want := []uuid.UUID{login.Id, billing.Id}; !slices.Equal(ids(got), want) {
		t.Fatalf("bob's results for login = %v, want %v", ids(got), want)
	}
	if got[0].InComment || !got[1].InComment || !strings.Contains(got[1].Snippet, "<mark>login</mark>") {
		t.Errorf("results = %+v, want the second to be a highlighted comment match", got)
	}
	if got := ids(search(ada, "login")); len(got) != 3 || !slices.Contains(got, hidden.Id) {
		t.Errorf("ada's results = %v, want all three issues", got)
	}

	// Words match by prefix and by stem, and all of them are required.
	if got := ids(search(bob, "pass res")); !slices.Equal(got, []uuid.UUID{login.Id}) {
		t.Errorf("prefix search = %v, want the login issue", got)
	}
	if got := ids(search(bob, "crash")); !slices.Equal(got, []uuid.UUID{login.Id}) {
		t.Errorf("stemmed search = %v, want the login issue", got)
	}
	if got := ids(search(bob, "invoice tax")); !slices.Equal(got, []uuid.UUID{billing.Id}) {
		t.Errorf("two-word search = %v, want only the billing issue", got)
	}
	if got := ids(search(bob, "invoice password")); len(got) != 0 {
		t.Errorf("words from different issues matched: %v", got)
	}

	// Nothing to search for, and syntax that would break a raw tsquery.
	for _, q := range []string{"%20", "!!!", "the", "a%26%7C!(b):*%27"} {
		var out api.SearchResults
		ada.expect(http.StatusOK, "GET", base+"/search?q="+q, nil, &out)
	}

	// Archived issues are still found, and say so.
	ada.expect(http.StatusOK, "DELETE", "/api/issues/"+login.Id.String(), nil, nil)
	if got := search(bob, "password"); len(got) != 1 || !got[0].Archived || got[0].Title != "Login page crashes" {
		t.Errorf("search for an archived issue = %+v", got)
	}

	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", base+"/search?q=login", nil, nil)
}

func TestAPIKeys(t *testing.T) {
	env := newTestEnv(t)
	ada, _, adaMe, _, ws, team := workspaceWithMember(t, env)
	other := ada.createWorkspace("Other", "other")
	base := "/api/workspaces/" + ws.Id.String()

	ada.expect(http.StatusBadRequest, "POST", base+"/api-keys", api.CreateApiKeyRequest{Name: " "}, nil)
	var created api.CreatedApiKey
	ada.expect(http.StatusCreated, "POST", base+"/api-keys", api.CreateApiKeyRequest{Name: "CI"}, &created)
	if !strings.HasPrefix(created.Token, "cruuze_") || !strings.HasSuffix(created.Token, created.ApiKey.Hint) || created.ApiKey.LastUsedAt != nil {
		t.Fatalf("created key = %+v", created)
	}

	// The key works without cookies, as its owner, in its workspace only.
	script := env.client(t)
	auth := []string{"Authorization", "Bearer " + created.Token}
	var me api.Me
	if got := script.do("GET", "/api/me", nil, &me, auth...); got != http.StatusOK {
		t.Fatalf("GET /me with key: status %d", got)
	}
	if me.User.Id != adaMe.User.Id || len(me.Workspaces) != 1 || me.Workspaces[0].Id != ws.Id {
		t.Errorf("me with key = %+v, want ada limited to one workspace", me)
	}
	var issue api.IssueMutationResult
	req := api.CreateIssueRequest{Id: newID(), Title: "From a script"}
	if got := script.do("POST", "/api/teams/"+team.Id.String()+"/issues", req, &issue, auth...); got != http.StatusCreated {
		t.Fatalf("create issue with key: status %d", got)
	}
	if issue.Issue.CreatorId == nil || *issue.Issue.CreatorId != adaMe.User.Id {
		t.Errorf("issue creator = %v, want ada", issue.Issue.CreatorId)
	}
	if got := script.do("GET", base+"/sync/bootstrap", nil, nil, auth...); got != http.StatusOK {
		t.Errorf("bootstrap with key: status %d", got)
	}
	if got := script.do("GET", "/api/workspaces/"+other.Id.String()+"/sync/bootstrap", nil, nil, auth...); got != http.StatusNotFound {
		t.Errorf("other workspace with key: status %d, want 404", got)
	}
	if len(script.cookies) != 0 {
		t.Errorf("a key request set cookies: %v", script.cookies)
	}

	// A key can't manage the account or other keys.
	for _, call := range [][2]string{
		{"GET", base + "/api-keys"},
		{"POST", base + "/api-keys"},
		{"DELETE", "/api/api-keys/" + created.ApiKey.Id.String()},
		{"PATCH", "/api/me"},
		{"POST", "/api/workspaces"},
		{"POST", "/api/auth/logout"},
	} {
		if got := script.do(call[0], call[1], map[string]any{"name": "x"}, nil, auth...); got != http.StatusForbidden {
			t.Errorf("%s %s with key: status %d, want 403", call[0], call[1], got)
		}
	}

	// Bad keys are rejected even alongside a valid cookie.
	if got := ada.do("GET", "/api/me", nil, nil, "Authorization", "Bearer cruuze_nope"); got != http.StatusUnauthorized {
		t.Errorf("bad key: status %d, want 401", got)
	}

	// Keys are private to their owner.
	var keys []api.ApiKey
	ada.expect(http.StatusOK, "GET", base+"/api-keys", nil, &keys)
	if len(keys) != 1 || keys[0].Name != "CI" || keys[0].LastUsedAt == nil {
		t.Errorf("ada's keys = %+v, want CI, used", keys)
	}
	bob, _ := env.signIn(t, "bob@example.com")
	bob.expect(http.StatusOK, "GET", base+"/api-keys", nil, &keys)
	if len(keys) != 0 {
		t.Errorf("bob sees %d of ada's keys", len(keys))
	}
	bob.expect(http.StatusNotFound, "DELETE", "/api/api-keys/"+created.ApiKey.Id.String(), nil, nil)

	ada.expect(http.StatusNoContent, "DELETE", "/api/api-keys/"+created.ApiKey.Id.String(), nil, nil)
	if got := script.do("GET", "/api/me", nil, nil, auth...); got != http.StatusUnauthorized {
		t.Errorf("deleted key: status %d, want 401", got)
	}
}

// endpoint is a fake webhook receiver, standing in for the network.
type endpoint struct {
	mu       sync.Mutex
	status   int
	requests []*http.Request
	bodies   [][]byte
}

func (e *endpoint) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests = append(e.requests, req)
	e.bodies = append(e.bodies, body)
	return &http.Response{StatusCode: e.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
}

// take returns the payloads received since the last call, in sync order.
func (e *endpoint) take(t *testing.T) []syncer.WebhookPayload {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]syncer.WebhookPayload, len(e.bodies))
	for i, body := range e.bodies {
		if err := json.Unmarshal(body, &out[i]); err != nil {
			t.Fatalf("webhook body: %v", err)
		}
	}
	e.requests, e.bodies = nil, nil
	slices.SortFunc(out, func(a, b syncer.WebhookPayload) int { return int(a.SyncID - b.SyncID) })
	return out
}

func (e *testEnv) deliver(t *testing.T) int {
	t.Helper()
	n, err := e.srv.deliverDueWebhooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWebhooks(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	receiver := &endpoint{status: http.StatusOK}
	env.srv.outbound = &http.Client{Transport: receiver}

	// Admins only, and only to public https endpoints.
	bob.expect(http.StatusForbidden, "GET", base+"/webhooks", nil, nil)
	bob.expect(http.StatusForbidden, "POST", base+"/webhooks", api.CreateWebhookRequest{Url: "https://hooks.example.com/x"}, nil)
	for _, bad := range []string{"", "hooks.example.com", "http://hooks.example.com/x", "https://127.0.0.1/x", "https://10.0.0.8/x",
		"https://[::1]/x", "https://169.254.169.254/latest", "https://localhost/x", "https://user:pw@hooks.example.com/x", "ftp://hooks.example.com"} {
		ada.expect(http.StatusBadRequest, "POST", base+"/webhooks", api.CreateWebhookRequest{Url: bad}, nil)
	}
	ada.expect(http.StatusBadRequest, "POST", base+"/webhooks",
		api.CreateWebhookRequest{Url: "https://hooks.example.com/x", Models: &[]api.SyncModel{api.SyncModelNotification}}, nil)

	var hook api.Webhook
	ada.expect(http.StatusCreated, "POST", base+"/webhooks", api.CreateWebhookRequest{Url: "https://hooks.example.com/cruuze"}, &hook)
	if hook.Label != "hooks.example.com" || !hook.Enabled || len(hook.Models) != len(syncer.WebhookModels) || !strings.HasPrefix(hook.Secret, "whsec_") {
		t.Fatalf("webhook = %+v", hook)
	}
	hookPath := "/api/webhooks/" + hook.Id.String()
	bob.expect(http.StatusForbidden, "PATCH", hookPath, api.UpdateWebhookRequest{}, nil)

	// Creating an issue and commenting on it sends both, signed.
	issue := ada.createIssue(team.Id, "Ship webhooks").Issue
	ada.expect(http.StatusCreated, "POST", "/api/issues/"+issue.Id.String()+"/comments", api.CreateCommentRequest{Id: newID(), Body: "Soon."}, nil)
	if n := env.deliver(t); n != 2 {
		t.Fatalf("delivered %d, want 2", n)
	}
	receiver.mu.Lock()
	first, firstBody := receiver.requests[0], receiver.bodies[0]
	receiver.mu.Unlock()
	if first.URL.String() != "https://hooks.example.com/cruuze" || first.Method != "POST" ||
		first.Header.Get("Cruuze-Signature") != "sha256="+signWebhook(hook.Secret, firstBody) ||
		first.Header.Get("Cruuze-Delivery") == "" || first.Header.Get("Content-Type") != "application/json" {
		t.Errorf("delivery request = %s %s %v", first.Method, first.URL, first.Header)
	}
	sent := receiver.take(t)
	if len(sent) != 2 || sent[0].Type != api.SyncModelIssue || sent[0].Action != "create" || sent[1].Type != api.SyncModelComment ||
		sent[0].WorkspaceID != ws.Id || sent[0].SyncID >= sent[1].SyncID {
		t.Fatalf("payloads = %+v", sent)
	}
	var data api.Issue
	if err := json.Unmarshal(sent[0].Data, &data); err != nil || data.Id != issue.Id || data.Title != "Ship webhooks" {
		t.Errorf("issue payload data = %s (%v)", sent[0].Data, err)
	}
	if n := env.deliver(t); n != 0 {
		t.Errorf("delivered %d again, want 0", n)
	}

	// Private teams and personal records (notifications, favorites) never go
	// to a workspace-wide webhook.
	private := true
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)
	hidden := ada.createIssue(secret.Id, "Hidden").Issue
	ada.expect(http.StatusCreated, "POST", base+"/favorites", api.CreateFavoriteRequest{Id: newID(), Kind: api.FavoriteKindIssue, TargetId: issue.Id}, nil)
	if n := env.deliver(t); n != 0 {
		t.Errorf("delivered %d private changes, want 0", n)
	}

	// A team webhook gets that team only, and only the models it asked for.
	bob.expect(http.StatusForbidden, "POST", base+"/webhooks", api.CreateWebhookRequest{Url: "https://hooks.example.com/secret", TeamId: &secret.Id}, nil)
	var teamHook api.Webhook
	ada.expect(http.StatusCreated, "POST", base+"/webhooks", api.CreateWebhookRequest{
		Url: "https://hooks.example.com/secret", TeamId: &secret.Id, Label: ptr("Secret issues"), Models: &[]api.SyncModel{api.SyncModelIssue},
	}, &teamHook)
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+hidden.Id.String(), map[string]any{"title": "Still hidden"}, nil)
	ada.expect(http.StatusCreated, "POST", "/api/issues/"+hidden.Id.String()+"/comments", api.CreateCommentRequest{Id: newID(), Body: "Shh."}, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"title": "Ship webhooks today"}, nil)
	env.deliver(t)
	receiver.mu.Lock()
	var urls []string
	for _, req := range receiver.requests {
		urls = append(urls, req.URL.Path)
	}
	receiver.mu.Unlock()
	slices.Sort(urls)
	if want := []string{"/cruuze", "/secret"}; !slices.Equal(urls, want) {
		t.Errorf("deliveries went to %v, want %v", urls, want)
	}
	receiver.take(t)

	// A failing endpoint is retried with backoff, then given up on.
	receiver.status = http.StatusInternalServerError
	ada.expect(http.StatusOK, "DELETE", "/api/issues/"+issue.Id.String(), nil, nil)
	for attempt := 1; attempt <= len(webhookBackoff)+1; attempt++ {
		if n := env.deliver(t); n != 1 {
			t.Fatalf("attempt %d: delivered %d, want 1", attempt, n)
		}
		if n := env.deliver(t); n != 0 {
			t.Fatalf("attempt %d was retried at once", attempt)
		}
		var deliveries []api.WebhookDelivery
		ada.expect(http.StatusOK, "GET", hookPath+"/deliveries", nil, &deliveries)
		latest := deliveries[0]
		wantStatus := api.WebhookDeliveryStatusPending
		if attempt == len(webhookBackoff)+1 {
			wantStatus = api.WebhookDeliveryStatusFailed
		}
		if latest.Status != wantStatus || int(latest.Attempts) != attempt || latest.ResponseStatus == nil || *latest.ResponseStatus != 500 || latest.Error == "" {
			t.Fatalf("attempt %d: delivery = %+v, want %s", attempt, latest, wantStatus)
		}
		// Pretend the backoff has passed.
		if _, err := env.pool.Exec(context.Background(), `UPDATE webhook_deliveries SET next_attempt_at = now() WHERE status = 'pending'`); err != nil {
			t.Fatal(err)
		}
	}
	if n := env.deliver(t); n != 0 {
		t.Errorf("a failed delivery was sent again")
	}
	receiver.take(t)
	receiver.status = http.StatusOK

	// The history is there for the admin, newest first.
	var deliveries []api.WebhookDelivery
	ada.expect(http.StatusOK, "GET", hookPath+"/deliveries", nil, &deliveries)
	if len(deliveries) != 4 || deliveries[0].Status != api.WebhookDeliveryStatusFailed || deliveries[3].Status != api.WebhookDeliveryStatusDelivered || deliveries[3].FinishedAt == nil {
		t.Errorf("deliveries = %+v", deliveries)
	}
	bob.expect(http.StatusForbidden, "GET", hookPath+"/deliveries", nil, nil)

	// A disabled webhook queues nothing; a deleted one is gone.
	var updated api.Webhook
	ada.expect(http.StatusOK, "PATCH", hookPath, api.UpdateWebhookRequest{Enabled: ptr(false), Label: ptr("Main")}, &updated)
	if updated.Enabled || updated.Label != "Main" || updated.Url != hook.Url {
		t.Errorf("updated webhook = %+v", updated)
	}
	ada.expect(http.StatusBadRequest, "PATCH", hookPath, api.UpdateWebhookRequest{Url: ptr("http://10.0.0.1/")}, nil)
	ada.createIssue(team.Id, "Unheard")
	if n := env.deliver(t); n != 0 {
		t.Errorf("a disabled webhook was sent %d deliveries", n)
	}
	ada.expect(http.StatusNoContent, "DELETE", hookPath, nil, nil)
	ada.expect(http.StatusNotFound, "DELETE", hookPath, nil, nil)
	var hooks []api.Webhook
	ada.expect(http.StatusOK, "GET", base+"/webhooks", nil, &hooks)
	if len(hooks) != 1 || hooks[0].Id != teamHook.Id || hooks[0].TeamId == nil {
		t.Errorf("webhooks = %+v, want only the team one", hooks)
	}
}

func ptr[T any](v T) *T { return &v }

func TestOutboundAddresses(t *testing.T) {
	public := []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946", "8.8.8.8"}
	private := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"::1", "fe80::1", "fd12:3456::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "224.0.0.1", "::"}
	for _, addr := range public {
		if !isPublicAddr(netip.MustParseAddr(addr)) {
			t.Errorf("%s should be allowed", addr)
		}
	}
	for _, addr := range private {
		if isPublicAddr(netip.MustParseAddr(addr)) {
			t.Errorf("%s should be refused", addr)
		}
	}
}

func TestIssueRefs(t *testing.T) {
	got := issueRefs("ada/eng-12-fix-login\nFix login (ENG-12, closes DES-3)\nSee utf-8 and eng-0012, not x-y or TOOLONGKEY-1.")
	want := []issueRef{{"ENG", 12}, {"DES", 3}, {"UTF", 8}}
	if !slices.Equal(got, want) {
		t.Errorf("issueRefs = %v, want %v", got, want)
	}
}

// githubEvent posts a signed pull_request event.
func (e *testEnv) githubEvent(workspaceID uuid.UUID, secret string, pr map[string]any) int {
	body, _ := json.Marshal(map[string]any{
		"action":       "opened",
		"pull_request": pr,
		"repository":   map[string]any{"full_name": "acme/app"},
	})
	return e.send("POST", "/api/integrations/github/"+workspaceID.String(), body,
		"Content-Type", "application/json",
		"X-GitHub-Event", "pull_request",
		"X-Hub-Signature-256", "sha256="+signWebhook(secret, body),
	).StatusCode
}

func TestGithubPullRequests(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	hookPath := "/api/integrations/github/" + ws.Id.String()

	// Not connected yet.
	if got := env.send("POST", hookPath, []byte("{}")).StatusCode; got != http.StatusNotFound {
		t.Errorf("event before connecting: status %d, want 404", got)
	}
	bob.expect(http.StatusForbidden, "PUT", base+"/integrations/github", nil, nil)
	var integration api.GithubIntegration
	ada.expect(http.StatusOK, "GET", base+"/integrations/github", nil, &integration)
	if integration.Enabled || integration.Secret != nil || integration.WebhookUrl != "http://app.test"+hookPath {
		t.Fatalf("integration = %+v", integration)
	}
	ada.expect(http.StatusOK, "PUT", base+"/integrations/github", nil, &integration)
	if !integration.Enabled || integration.Secret == nil {
		t.Fatalf("enabled integration = %+v", integration)
	}
	secret := *integration.Secret

	// Bob follows the issue, so he hears about its status.
	var created api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/issues",
		api.CreateIssueRequest{Id: newID(), Title: "Fix login", AssigneeId: &bobMe.User.Id}, &created)
	issue := created.Issue
	other := ada.createIssue(team.Id, "Unrelated").Issue
	stateType := func(issueID uuid.UUID) api.WorkflowStateType {
		t.Helper()
		boot := ada.bootstrap(ws.Id)
		for _, i := range boot.Issues {
			if i.Id == issueID {
				for _, s := range boot.WorkflowStates {
					if s.Id == i.StateId {
						return s.Type
					}
				}
			}
		}
		t.Fatalf("issue %s not found", issueID)
		return ""
	}

	pr := map[string]any{
		"html_url": "https://github.com/acme/app/pull/7", "number": 7, "title": "Fix the login redirect",
		"body": nil, "state": "open", "draft": true, "merged": false,
		"head": map[string]any{"ref": "ada/acm-1-fix-login"}, "user": map[string]any{"login": "ada"},
	}

	// Unsigned, mis-signed and unrelated events change nothing.
	body, _ := json.Marshal(map[string]any{"pull_request": pr})
	if got := env.send("POST", hookPath, body, "X-GitHub-Event", "pull_request").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("unsigned event: status %d, want 401", got)
	}
	if got := env.githubEvent(ws.Id, "wrong", pr); got != http.StatusUnauthorized {
		t.Errorf("mis-signed event: status %d, want 401", got)
	}
	ping := []byte(`{"zen":"Keep it logically awesome."}`)
	if got := env.send("POST", hookPath, ping, "X-GitHub-Event", "ping", "X-Hub-Signature-256", "sha256="+signWebhook(secret, ping)).StatusCode; got != http.StatusNoContent {
		t.Errorf("ping: status %d, want 204", got)
	}
	if got := len(ada.bootstrap(ws.Id).PullRequests); got != 0 {
		t.Fatalf("%d pull requests linked by rejected events", got)
	}

	// A draft PR whose branch names the issue links it and starts the work.
	if got := env.githubEvent(ws.Id, secret, pr); got != http.StatusNoContent {
		t.Fatalf("pull_request event: status %d, want 204", got)
	}
	linked := ada.bootstrap(ws.Id).PullRequests
	if len(linked) != 1 || linked[0].IssueId != issue.Id || linked[0].Status != api.PullRequestStatusDraft || linked[0].Repo != "acme/app" ||
		linked[0].Number != 7 || linked[0].Branch != "ada/acm-1-fix-login" || linked[0].Author != "ada" {
		t.Fatalf("pull requests = %+v", linked)
	}
	if got := stateType(issue.Id); got != api.WorkflowStateTypeStarted {
		t.Errorf("issue is %s after its PR opened, want started", got)
	}
	if got := stateType(other.Id); got != api.WorkflowStateTypeBacklog {
		t.Errorf("unrelated issue moved to %s", got)
	}
	if got := bob.notificationTypes(ws.Id); !slices.Equal(got, []api.NotificationType{api.NotificationTypeAssigned, api.NotificationTypeStatus}) {
		t.Errorf("bob's notifications = %v, want [assigned status]", got)
	}

	// The same event again is a no-op; an edit updates the link in place.
	before := ada.lastSyncID(ws.Id)
	env.githubEvent(ws.Id, secret, pr)
	if after := ada.lastSyncID(ws.Id); after != before {
		t.Errorf("a repeated event wrote %d sync actions", after-before)
	}
	pr["draft"], pr["title"] = false, "Fix the login redirect (ACM-2)"
	env.githubEvent(ws.Id, secret, pr)
	linked = ada.bootstrap(ws.Id).PullRequests
	if len(linked) != 2 {
		t.Fatalf("%d links after the title named a second issue, want 2", len(linked))
	}
	for _, l := range linked {
		if l.Status != api.PullRequestStatusOpen || l.Title != "Fix the login redirect (ACM-2)" {
			t.Errorf("link = %+v, want open with the new title", l)
		}
	}

	// A second open PR keeps the issue from completing when the first merges.
	second := map[string]any{
		"html_url": "https://github.com/acme/app/pull/8", "number": 8, "title": "ACM-1 follow-up", "state": "open",
		"head": map[string]any{"ref": "follow-up"}, "user": map[string]any{"login": "bob"},
	}
	env.githubEvent(ws.Id, secret, second)
	pr["state"], pr["merged"] = "closed", true
	env.githubEvent(ws.Id, secret, pr)
	if got := stateType(issue.Id); got != api.WorkflowStateTypeStarted {
		t.Errorf("issue is %s with a PR still open, want started", got)
	}
	if got := stateType(other.Id); got != api.WorkflowStateTypeCompleted {
		t.Errorf("second issue is %s after its only PR merged, want completed", got)
	}
	// Closing the last one without merging leaves the issue alone; merging completes it.
	second["state"] = "closed"
	env.githubEvent(ws.Id, secret, second)
	if got := stateType(issue.Id); got != api.WorkflowStateTypeStarted {
		t.Errorf("issue is %s after a PR was closed unmerged, want started", got)
	}
	second["merged"] = true
	env.githubEvent(ws.Id, secret, second)
	if got := stateType(issue.Id); got != api.WorkflowStateTypeCompleted {
		t.Errorf("issue is %s after its last PR merged, want completed", got)
	}

	// Links are scoped like the issue: outsiders to a private team don't see them.
	private := true
	var secretTeam api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secretTeam)
	ada.createIssue(secretTeam.Id, "Hidden")
	env.githubEvent(ws.Id, secret, map[string]any{
		"html_url": "https://github.com/acme/app/pull/9", "number": 9, "title": "SEC-1", "state": "open",
		"head": map[string]any{"ref": "x"}, "user": map[string]any{"login": "ada"},
	})
	if got := len(ada.bootstrap(ws.Id).PullRequests); got != 4 {
		t.Errorf("ada sees %d links, want 4", got)
	}
	if got := len(bob.bootstrap(ws.Id).PullRequests); got != 3 {
		t.Errorf("bob sees %d links, want 3", got)
	}

	// A javascript: URL would become a link in the app.
	if got := env.githubEvent(ws.Id, secret, map[string]any{"html_url": "javascript:alert(1)", "number": 1, "title": "ACM-1", "state": "open"}); got != http.StatusBadRequest {
		t.Errorf("unsafe URL: status %d, want 400", got)
	}

	// Rotating the secret invalidates the old one; disconnecting stops events.
	ada.expect(http.StatusOK, "PUT", base+"/integrations/github", nil, &integration)
	if *integration.Secret == secret {
		t.Error("the secret was not rotated")
	}
	if got := env.githubEvent(ws.Id, secret, pr); got != http.StatusUnauthorized {
		t.Errorf("old secret: status %d, want 401", got)
	}
	ada.expect(http.StatusNoContent, "DELETE", base+"/integrations/github", nil, nil)
	if got := env.githubEvent(ws.Id, *integration.Secret, pr); got != http.StatusNotFound {
		t.Errorf("event after disconnecting: status %d, want 404", got)
	}
}

func TestImportIssues(t *testing.T) {
	env := newTestEnv(t)
	ada, _, _, bobMe, ws, team := workspaceWithMember(t, env)
	importPath := "/api/teams/" + team.Id.String() + "/import"
	ada.createIssue(team.Id, "Already here")

	post := func(c *client, csv string) (int, api.ImportResult) {
		t.Helper()
		res := c.raw("POST", importPath, strings.NewReader(csv), "text/csv")
		var out api.ImportResult
		if res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
		}
		return res.StatusCode, out
	}

	if status, _ := post(ada, ""); status != http.StatusBadRequest {
		t.Errorf("empty file: status %d, want 400", status)
	}
	if status, _ := post(ada, "Summary of things\nx\n"); status != http.StatusBadRequest {
		t.Errorf("no title column: status %d, want 400", status)
	}
	if status, _ := post(ada, "Title\n\xff\xfe\n"); status != http.StatusBadRequest {
		t.Errorf("not UTF-8: status %d, want 400", status)
	}

	file := "\xef\xbb\xbfTitle,Description,Status,Priority,Assignee,Labels,Estimate,Due Date,Ignored\n" +
		"Set up CI,\"Runs on every push.\nIncluding forks, \"\"safely\"\".\",In Progress,High,bob@example.com,\"infra, Tooling\",3,2026-11-05,x\n" +
		"Write docs,,done,4,Nobody Inparticular,infra,lots,soon,\n" +
		",No title here,,,,,,,\n" +
		"\n" +
		"Plan launch,,Someday,urgent,,,,11/20/2026\n"
	status, result := post(ada, file)
	if status != http.StatusOK || result.Created != 3 || result.LabelsCreated != 2 || len(result.Skipped) != 1 || result.Skipped[0].Row != 5 {
		t.Fatalf("import: status %d, result %+v", status, result)
	}

	boot := ada.bootstrap(ws.Id)
	byTitle := map[string]api.Issue{}
	for _, issue := range boot.Issues {
		byTitle[issue.Title] = issue
	}
	stateTypes := map[uuid.UUID]api.WorkflowStateType{}
	for _, s := range boot.WorkflowStates {
		stateTypes[s.Id] = s.Type
	}
	labelNames := map[uuid.UUID]string{}
	for _, l := range boot.Labels {
		labelNames[l.Id] = l.Name
	}

	ci := byTitle["Set up CI"]
	if ci.Number != 2 || stateTypes[ci.StateId] != api.WorkflowStateTypeStarted || ci.Priority != 2 ||
		ci.AssigneeId == nil || *ci.AssigneeId != bobMe.User.Id || !slices.Contains(ci.SubscriberIds, bobMe.User.Id) ||
		ci.DueDate == nil || ci.DueDate.Format("2006-01-02") != "2026-11-05" ||
		ci.Description != "Runs on every push.\nIncluding forks, \"safely\"." {
		t.Errorf("imported issue = %+v", ci)
	}
	if len(ci.LabelIds) != 2 || labelNames[ci.LabelIds[0]] != "infra" || labelNames[ci.LabelIds[1]] != "Tooling" {
		t.Errorf("labels = %v (%v)", ci.LabelIds, labelNames)
	}
	docs := byTitle["Write docs"]
	if stateTypes[docs.StateId] != api.WorkflowStateTypeCompleted || docs.Priority != 4 || docs.AssigneeId != nil || docs.DueDate != nil || len(docs.LabelIds) != 1 {
		t.Errorf("imported issue = %+v", docs)
	}
	launch := byTitle["Plan launch"]
	if stateTypes[launch.StateId] != api.WorkflowStateTypeBacklog || launch.Priority != 1 || launch.DueDate == nil || launch.DueDate.Format("2006-01-02") != "2026-11-20" {
		t.Errorf("imported issue = %+v", launch)
	}
	// File order, above what was there.
	if !(ci.SortOrder < docs.SortOrder && docs.SortOrder < launch.SortOrder && launch.SortOrder < byTitle["Already here"].SortOrder) {
		t.Errorf("sort orders = %v %v %v, existing %v", ci.SortOrder, docs.SortOrder, launch.SortOrder, byTitle["Already here"].SortOrder)
	}
	// The next issue made by hand continues the numbering.
	if next := ada.createIssue(team.Id, "After").Issue; next.Number != 5 {
		t.Errorf("issue number after import = %d, want 5", next.Number)
	}

	// Semicolons, other header names, and a label that now exists.
	status, result = post(ada, "Summary;Tags;State\nFrom a spreadsheet;INFRA;Todo\n")
	if status != http.StatusOK || result.Created != 1 || result.LabelsCreated != 0 {
		t.Fatalf("semicolon import: status %d, result %+v", status, result)
	}

	// Guests can import into their team but can't create labels.
	guest := api.WorkspaceRoleGuest
	ada.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/invites", api.CreateInviteRequest{Email: "gus@example.com", Role: &guest}, nil)
	gus, gusMe := env.signIn(t, "gus@example.com")
	if status, _ := post(gus, "Title\nSneaky\n"); status != http.StatusNotFound {
		t.Errorf("import by an outsider to the team: status %d, want 404", status)
	}
	ada.expect(http.StatusNoContent, "PUT", "/api/teams/"+team.Id.String()+"/members/"+gusMe.User.Id.String(), nil, nil)
	status, result = post(gus, "Title,Labels\nFrom a guest,\"infra,brand-new\"\n")
	if status != http.StatusOK || result.Created != 1 || result.LabelsCreated != 0 {
		t.Errorf("guest import: status %d, result %+v", status, result)
	}

	// Too many rows is refused whole.
	if status, _ := post(ada, "Title\n"+strings.Repeat("x\n", maxImportRows+1)); status != http.StatusBadRequest {
		t.Errorf("oversized import: status %d, want 400", status)
	}
}

func TestNotificationEmails(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, bobMe, ws, team := workspaceWithMember(t, env)
	ada.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"name": "Ada"}, nil)
	ctx := context.Background()
	soon := time.Now().Add(time.Minute)

	send := func(before time.Time) int {
		t.Helper()
		n, err := env.srv.sendNotificationEmails(ctx, before)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	var created api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/issues",
		api.CreateIssueRequest{Id: newID(), Title: "Fix login", AssigneeId: &bobMe.User.Id}, &created)
	issuePath := "/api/issues/" + created.Issue.Id.String()

	// Not yet: the notification is newer than the delay.
	if n := send(time.Now().Add(-time.Hour)); n != 0 {
		t.Fatalf("sent %d emails before the delay passed", n)
	}
	if n := send(soon); n != 1 {
		t.Fatalf("sent %d emails, want 1", n)
	}
	email := env.mail.lastTo(t, "bob@example.com")
	if email.Subject != "Ada assigned ACM-1 to you: Fix login" || !strings.Contains(email.Text, "http://app.test/acme/issue/ACM-1") ||
		!strings.Contains(email.Text, "http://app.test/acme/settings/account") {
		t.Errorf("email = %q\n%s", email.Subject, email.Text)
	}
	// Once only.
	if n := send(soon); n != 0 {
		t.Errorf("sent %d emails for an already emailed notification", n)
	}

	// Several unread notifications make one digest; read ones are left out.
	ada.expect(http.StatusCreated, "POST", issuePath+"/comments",
		api.CreateCommentRequest{Id: newID(), Body: "Can you look, " + mention(bobMe.User) + "?\n\nIt is **urgent**."}, nil)
	ada.expect(http.StatusCreated, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "Never mind."}, nil)
	ada.expect(http.StatusCreated, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "Read in the app."}, nil)
	newest := bob.bootstrap(ws.Id).Notifications[0]
	bob.expect(http.StatusOK, "PATCH", "/api/notifications/"+newest.Id.String(), api.UpdateNotificationRequest{Read: true}, nil)
	if n := send(soon); n != 1 {
		t.Fatalf("sent %d emails, want one digest", n)
	}
	email = env.mail.lastTo(t, "bob@example.com")
	if email.Subject != "2 new notifications in Acme" || !strings.Contains(email.Text, "Ada mentioned you in ACM-1") ||
		!strings.Contains(email.Text, "> Can you look, @"+bobMe.User.Name+"? It is **urgent**.") ||
		!strings.Contains(email.Text, "Ada commented on ACM-1") || strings.Contains(email.Text, "Read in the app.") {
		t.Errorf("digest = %q\n%s", email.Subject, email.Text)
	}

	// Opting out stops the emails but not the notifications.
	var me api.Me
	bob.expect(http.StatusOK, "GET", "/api/me", nil, &me)
	if !me.EmailNotifications {
		t.Error("email notifications should default to on")
	}
	bob.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"emailNotifications": false}, nil)
	bob.expect(http.StatusOK, "GET", "/api/me", nil, &me)
	if me.EmailNotifications || me.User.Name != bobMe.User.Name {
		t.Errorf("me after opting out = %+v", me)
	}
	ada.expect(http.StatusCreated, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "Unheard."}, nil)
	sentBefore := len(env.mail.messages)
	if n := send(soon); n != 0 || len(env.mail.messages) != sentBefore {
		t.Errorf("sent %d emails to someone who opted out", n)
	}
	if got := len(bob.bootstrap(ws.Id).Notifications); got != 5 {
		t.Errorf("bob has %d notifications, want 5", got)
	}

	// Ada still gets hers.
	bob.expect(http.StatusCreated, "POST", issuePath+"/comments", api.CreateCommentRequest{Id: newID(), Body: "Done."}, nil)
	if n := send(soon); n != 1 {
		t.Errorf("sent %d emails, want 1 to ada", n)
	}
	if email := env.mail.lastTo(t, adaMe.User.Email); !strings.Contains(email.Subject, "commented on ACM-1") {
		t.Errorf("ada's email = %q", email.Subject)
	}
}
