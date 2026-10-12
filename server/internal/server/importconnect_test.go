package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/seal"
)

// tracker stands in for the trackers' sign-in and API hosts. It answers by
// host and path, and remembers what it was asked.
type tracker struct {
	mu       sync.Mutex
	answers  map[string]string // "host/path" → body
	forms    []url.Values      // bodies posted to token endpoints
	bearers  []string          // Authorization headers of API requests
	fallback http.RoundTripper
}

func (tr *tracker) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/oauth/") {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		tr.forms = append(tr.forms, form)
	} else {
		tr.bearers = append(tr.bearers, req.Header.Get("Authorization"))
	}
	if body, ok := tr.answers[req.URL.Host+req.URL.Path]; ok {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	}
	return tr.fallback.RoundTrip(req)
}

// withConnections sets a test server up to keep secrets and to connect to
// GitHub and Jira.
func withConnections(t *testing.T, env *testEnv) {
	t.Helper()
	key, err := seal.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	env.srv.sealKey = key
	env.srv.cfg.ImportGitHubClientID, env.srv.cfg.ImportGitHubClientSecret = "gh-client", "gh-secret"
	env.srv.cfg.ImportAtlassianClientID, env.srv.cfg.ImportAtlassianClientSecret = "at-client", "at-secret"
	env.handler = env.srv.Handler()
}

// connect walks a client through connecting a tracker and returns where it
// ended up.
func (c *client) connect(workspaceID, provider string) string {
	c.t.Helper()
	res := c.raw("GET", "/api/workspaces/"+workspaceID+"/imports/connect/"+provider, nil, "")
	if res.StatusCode != http.StatusFound {
		c.t.Fatalf("starting to connect %s: status %d", provider, res.StatusCode)
	}
	for _, ck := range res.Cookies() {
		c.cookies[ck.Name] = ck.Value
	}
	signIn, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		c.t.Fatal(err)
	}
	state := signIn.Query().Get("state")
	if state == "" || signIn.Query().Get("redirect_uri") != "http://app.test/api/imports/connect/"+provider+"/callback" {
		c.t.Fatalf("sign-in address = %s", signIn)
	}
	res = c.raw("GET", "/api/imports/connect/"+provider+"/callback?code=the-code&state="+state, nil, "")
	if res.StatusCode != http.StatusFound {
		c.t.Fatalf("coming back from %s: status %d", provider, res.StatusCode)
	}
	return res.Header.Get("Location")
}

func TestConnectGitHubAndImportThroughIt(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, _ := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String() + "/imports"

	// A server that isn't set up for it offers nothing.
	var conns api.ImportConnections
	ada.expect(http.StatusOK, "GET", base+"/connections", nil, &conns)
	if len(conns.Available) != 0 || conns.Resumable {
		t.Fatalf("connections without a key = %+v", conns)
	}
	if res := ada.raw("GET", base+"/connect/github", nil, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("connecting without a key: status %d", res.StatusCode)
	}

	withConnections(t, env)
	repo := "/repos/acme/widgets"
	github := &tracker{
		answers: map[string]string{
			"github.com/login/oauth/access_token": `{"access_token":"gho_secret","token_type":"bearer","scope":"repo"}`,
			"api.github.com/user":                 `{"login":"ada-dev"}`,
			"api.github.com/user/repos":           `[{"full_name":"acme/widgets","has_issues":true},{"full_name":"acme/wiki","has_issues":false}]`,
		},
		fallback: cannedAPI{
			"GET " + repo: `{"full_name":"acme/widgets","has_issues":true}`,
			"GET /search/issues?per_page=1&q=repo%3Aacme%2Fwidgets+is%3Aissue": `{"total_count":1}`,
			"GET " + repo + "/assignees?per_page=100":                          `[]`,
			"GET " + repo + "/labels?per_page=100":                             `[]`,
			"GET " + repo + "/milestones?per_page=100&state=all":               `[]`,
			"GET " + repo + "/issues?direction=asc&per_page=100&sort=created&state=all": `[
				{"number":1,"title":"Private thing","state":"open","user":{"login":"ada-dev"},"labels":[],
				 "created_at":"2026-09-01T09:00:00Z","updated_at":"2026-09-01T09:00:00Z"}]`,
			"GET " + repo + "/issues/comments?direction=asc&per_page=100&sort=created": `[]`,
		},
	}
	env.srv.importHTTP = &http.Client{Transport: github}

	// Admins only; and the answer must come back to the browser that asked.
	if res := bob.raw("GET", base+"/connect/github", nil, ""); res.StatusCode != http.StatusForbidden {
		t.Errorf("a member connecting: status %d", res.StatusCode)
	}
	if res := ada.raw("GET", "/api/imports/connect/github/callback?code=x&state=made-up", nil, ""); !strings.Contains(res.Header.Get("Location"), "connect_error=github") {
		t.Errorf("an answer nobody asked for went to %q", res.Header.Get("Location"))
	}
	ada.raw("GET", base+"/connect/github", nil, "") // sets no cookie on the client: raw keeps none
	if res := ada.raw("GET", "/api/imports/connect/github/callback?code=x&state=wrong", nil, ""); !strings.Contains(res.Header.Get("Location"), "connect_error=github") {
		t.Errorf("a wrong state went to %q", res.Header.Get("Location"))
	}

	if at := ada.connect(ws.Id.String(), "github"); at != "http://app.test/acme/settings/import?connected=github" {
		t.Fatalf("came back to %q", at)
	}
	if len(github.forms) != 1 || github.forms[0].Get("code") != "the-code" || github.forms[0].Get("client_secret") != "gh-secret" || github.forms[0].Get("grant_type") != "authorization_code" {
		t.Errorf("token request = %v", github.forms)
	}
	ada.expect(http.StatusOK, "GET", base+"/connections", nil, &conns)
	if len(conns.Available) != 2 || len(conns.Connections) != 1 || conns.Connections[0].Provider != "github" || conns.Connections[0].Label != "ada-dev" || !conns.Resumable {
		t.Fatalf("connections = %+v", conns)
	}
	// The token is kept, but not in the clear, and is ada's alone.
	var stored []byte
	if err := env.pool.QueryRow(context.Background(), `SELECT secret FROM import_connections WHERE workspace_id = $1`, ws.Id).Scan(&stored); err != nil || bytes.Contains(stored, []byte("gho_secret")) {
		t.Errorf("stored secret holds the token in the clear (%v)", err)
	}
	ada.expect(http.StatusOK, "PATCH", "/api/workspaces/"+ws.Id.String()+"/members/"+bob.me().User.Id.String(), api.UpdateMemberRequest{Role: api.WorkspaceRoleAdmin}, nil)
	var bobs api.ImportConnections
	bob.expect(http.StatusOK, "GET", base+"/connections", nil, &bobs)
	if len(bobs.Connections) != 0 {
		t.Errorf("bob sees ada's connection: %+v", bobs.Connections)
	}
	request := map[string]any{"source": "github", "github": map[string]any{"repo": "acme/widgets"}, "useConnection": true}
	bob.expect(http.StatusBadRequest, "POST", base, request, nil)

	// The repositories it can read, and an import with no token in sight.
	var repos []string
	ada.expect(http.StatusOK, "GET", base+"/connections/github/repos", nil, &repos)
	if len(repos) != 1 || repos[0] != "acme/widgets" {
		t.Errorf("repos = %v", repos)
	}
	github.bearers = nil
	done := ada.runImport(ws.Id, request)
	if done.Status != api.ImportStatusDone || done.Counts["issues"] != 1 {
		t.Fatalf("import = %+v", done)
	}
	for _, bearer := range github.bearers {
		if bearer != "Bearer gho_secret" {
			t.Fatalf("a request went out with %q", bearer)
		}
	}
	// Nothing of it is kept once the import is over.
	var kept *[]byte
	if err := env.pool.QueryRow(context.Background(), `SELECT secret FROM imports WHERE id = $1`, done.Id).Scan(&kept); err != nil || kept != nil {
		t.Errorf("the finished import still holds its request (%v)", err)
	}

	ada.expect(http.StatusNoContent, "DELETE", base+"/connections/github", nil, nil)
	ada.expect(http.StatusBadRequest, "POST", base, request, nil)
}

func (c *client) me() api.Me {
	c.t.Helper()
	var me api.Me
	c.expect(http.StatusOK, "GET", "/api/me", nil, &me)
	return me
}

func TestJiraConnectionRefreshesItsToken(t *testing.T) {
	env := newTestEnv(t)
	ada, _, adaMe, _, ws, _ := workspaceWithMember(t, env)
	withConnections(t, env)
	atlassian := &tracker{
		answers: map[string]string{
			"auth.atlassian.com/oauth/token": `{"access_token":"first","refresh_token":"refresh-1","expires_in":3600}`,
			"api.atlassian.com/oauth/token/accessible-resources": `[
				{"id":"conf-9","url":"https://acme.atlassian.net","name":"acme","scopes":["read:confluence-content.all"]},
				{"id":"cloud-1","url":"https://acme.atlassian.net","name":"acme","scopes":["read:jira-work","read:jira-user"]}]`,
		},
		fallback: cannedAPI{},
	}
	env.srv.importHTTP = &http.Client{Transport: atlassian}
	if at := ada.connect(ws.Id.String(), "jira"); !strings.HasSuffix(at, "?connected=jira") {
		t.Fatalf("came back to %q", at)
	}
	ctx := context.Background()
	conn, secret, err := env.srv.importConnection(ctx, ws.Id, adaMe.User.Id, "jira")
	if err != nil || conn.Label != "acme.atlassian.net" || secret.CloudID != "cloud-1" || secret.Site != "https://acme.atlassian.net" || secret.Access != "first" || secret.Refresh != "refresh-1" {
		t.Fatalf("connection = %+v, secret = %+v (%v)", conn.Label, secret, err)
	}

	// While it lasts, the token is used as it is.
	token := env.srv.connectionToken(conn, secret)
	if got, err := token(ctx, ""); err != nil || got != "first" || len(atlassian.forms) != 1 {
		t.Fatalf("token = %q, %v after %d token requests", got, err, len(atlassian.forms))
	}
	// Refused, or about to run out, it is exchanged for a new one, which is kept.
	atlassian.answers["auth.atlassian.com/oauth/token"] = `{"access_token":"second","refresh_token":"refresh-2","expires_in":3600}`
	if got, err := token(ctx, "first"); err != nil || got != "second" {
		t.Fatalf("token after a refusal = %q, %v", got, err)
	}
	last := atlassian.forms[len(atlassian.forms)-1]
	if last.Get("grant_type") != "refresh_token" || last.Get("refresh_token") != "refresh-1" || last.Get("client_id") != "at-client" {
		t.Errorf("refresh request = %v", last)
	}
	if _, kept, err := env.srv.importConnection(ctx, ws.Id, adaMe.User.Id, "jira"); err != nil || kept.Access != "second" || kept.Refresh != "refresh-2" || kept.CloudID != "cloud-1" {
		t.Errorf("kept after refreshing = %+v (%v)", kept, err)
	}
	secret.Expires = time.Now().Add(time.Minute)
	atlassian.answers["auth.atlassian.com/oauth/token"] = `{"access_token":"third","expires_in":3600}`
	if got, err := env.srv.connectionToken(conn, secret)(ctx, ""); err != nil || got != "third" {
		t.Errorf("token when about to run out = %q, %v", got, err)
	}
	// When it can't be renewed, the import is told to connect again.
	atlassian.answers["auth.atlassian.com/oauth/token"] = `{"error":"invalid_grant"}`
	if _, err := env.srv.connectionToken(conn, secret)(ctx, secret.Access); err == nil || !strings.Contains(err.Error(), "Connect") {
		t.Errorf("err = %v, want one saying to connect again", err)
	}
}

func TestImportCarriesOnAfterARestart(t *testing.T) {
	env := newTestEnv(t)
	ada, _, adaMe, _, ws, _ := workspaceWithMember(t, env)
	withConnections(t, env)
	ctx := context.Background()
	repo := "/repos/acme/widgets"
	// (A later run asks only for what changed, which here is answered alike.)
	env.srv.importHTTP = &http.Client{Transport: &sinceAPI{api: cannedAPI{
		"GET " + repo:                                        `{"full_name":"acme/widgets","has_issues":true}`,
		"GET " + repo + "/labels?per_page=100":               `[]`,
		"GET " + repo + "/milestones?per_page=100&state=all": `[]`,
		"GET " + repo + "/issues?direction=asc&per_page=100&sort=created&state=all": `[
			{"number":1,"title":"Half way","state":"open","user":{"login":"octocat"},"labels":[],
			 "created_at":"2026-09-01T09:00:00Z","updated_at":"2026-09-01T09:00:00Z"}]`,
		"GET " + repo + "/issues/comments?direction=asc&per_page=100&sort=created": `[]`,
	}}}

	// An import as a server that died left it: running, unheard from, with
	// its request (token and all) sealed beside it.
	leave := func(resumes int) dbgen.Import {
		t.Helper()
		id := newID()
		whole, _ := json.Marshal(map[string]any{"source": "github", "github": map[string]any{"repo": "acme/widgets", "token": "ghp_typed"}})
		sealed, err := env.srv.sealKey.Seal(whole, importSecretBound(id))
		if err != nil || bytes.Contains(sealed, []byte("ghp_typed")) {
			t.Fatalf("sealing: %v", err)
		}
		imp, err := env.q.CreateImport(ctx, dbgen.CreateImportParams{
			ID: id, WorkspaceID: ws.Id, Source: "github", Label: "acme/widgets", Stage: "Reading", CreatedBy: &adaMe.User.Id,
			Request: []byte(`{"source":"github","github":{"repo":"acme/widgets"}}`), Origin: "github:acme/widgets", Secret: sealed,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.pool.Exec(ctx, `UPDATE imports SET heartbeat_at = now() - interval '10 minutes', resumes = $2 WHERE id = $1`, id, resumes); err != nil {
			t.Fatal(err)
		}
		return imp
	}
	get := func(imp dbgen.Import) api.Import {
		t.Helper()
		var out api.Import
		ada.expect(http.StatusOK, "GET", "/api/imports/"+imp.ID.String(), nil, &out)
		return out
	}

	left := leave(0)
	// It still counts as running: nothing else may start meanwhile.
	ada.expect(http.StatusConflict, "POST", "/api/workspaces/"+ws.Id.String()+"/imports",
		map[string]any{"source": "github", "github": map[string]any{"repo": "acme/widgets"}}, nil)
	if n, err := env.srv.resumeImports(ctx); err != nil || n != 1 {
		t.Fatalf("resumed %d imports (%v), want 1", n, err)
	}
	env.srv.imports.Wait()
	if done := get(left); done.Status != api.ImportStatusDone || done.Counts["issues"] != 1 {
		t.Fatalf("resumed import = %+v", done)
	}
	if n, _ := env.srv.resumeImports(ctx); n != 0 {
		t.Errorf("resumed %d imports again", n)
	}

	// One that has been tried enough is given up on instead.
	spent := leave(3)
	if n, _ := env.srv.resumeImports(ctx); n != 0 {
		t.Errorf("resumed an import that had been tried 3 times")
	}
	if failed := get(spent); failed.Status != api.ImportStatusFailed {
		t.Errorf("spent import = %+v", failed)
	}

	// A server that shuts down hands its import on, to be picked up at once.
	handed := leave(0)
	if _, err := env.pool.Exec(ctx, `UPDATE imports SET heartbeat_at = now() WHERE id = $1`, handed.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := env.srv.resumeImports(ctx); n != 0 {
		t.Fatal("resumed an import that is being heard from")
	}
	if err := env.q.ReleaseImport(ctx, handed.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := env.srv.resumeImports(ctx); n != 1 {
		t.Fatal("an import handed on wasn't picked up")
	}
	env.srv.imports.Wait()
	if done := get(handed); done.Status != api.ImportStatusDone || done.Counts["unchanged"] != 1 {
		t.Errorf("handed-on import = %+v", done)
	}

	// Whoever started it must still be an admin when it carries on.
	orphan := leave(0)
	if _, err := env.pool.Exec(ctx, `UPDATE workspace_members SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`, ws.Id, adaMe.User.Id); err != nil {
		t.Fatal(err)
	}
	if n, _ := env.srv.resumeImports(ctx); n != 0 {
		t.Error("resumed an import whose starter is no longer an admin")
	}
	var status string
	var secret *[]byte
	if err := env.pool.QueryRow(ctx, `SELECT status, secret FROM imports WHERE id = $1`, orphan.ID).Scan(&status, &secret); err != nil || status != "failed" || secret != nil {
		t.Errorf("orphaned import: status %q, secret kept %v (%v)", status, secret != nil, err)
	}
}
