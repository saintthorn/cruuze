package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"cruuze/server/internal/api"
	"cruuze/server/internal/blob"
	"cruuze/server/internal/config"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/mail"
)

// captureSender records outgoing email so tests can follow magic links.
type captureSender struct {
	mu       sync.Mutex
	messages []mail.Message
}

func (c *captureSender) Send(_ context.Context, m mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, m)
	return nil
}

func (c *captureSender) lastTo(t *testing.T, to string) mail.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].To == to {
			return c.messages[i]
		}
	}
	t.Fatalf("no email sent to %s", to)
	return mail.Message{}
}

type testEnv struct {
	srv     *Server
	handler http.Handler
	mail    *captureSender
	q       *dbgen.Queries
	pool    *pgxpool.Pool
}

// newTestEnv returns a server backed by an empty database. Tests are skipped
// unless TEST_DATABASE_URL points at a disposable Postgres database.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `TRUNCATE users, workspaces, login_tokens CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE instance_settings SET signup = 'open', signup_domains = '{}', workspace_creation = 'anyone'`)
	if err != nil {
		t.Fatalf("reset instance settings: %v", err)
	}

	sender := &captureSender{}
	cfg := config.Config{DatabaseURL: url, AppURL: "http://app.test"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	blobs, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, pool, sender, blobs, log, nil)
	return &testEnv{srv: srv, handler: srv.Handler(), mail: sender, q: dbgen.New(pool), pool: pool}
}

// client is a browser-like API client: it keeps cookies between requests.
type client struct {
	t       *testing.T
	env     *testEnv
	cookies map[string]string
}

func (e *testEnv) client(t *testing.T) *client {
	return &client{t: t, env: e, cookies: map[string]string{}}
}

// do sends a JSON request and decodes a JSON response into out (if non-nil),
// returning the status code.
func (c *client) do(method, path string, body, out any, headers ...string) int {
	c.t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reqBody = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "http://app.test"+path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	c.env.handler.ServeHTTP(rec, req)
	res := rec.Result()
	for _, ck := range res.Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			c.t.Fatalf("%s %s: decode response (status %d): %v", method, path, res.StatusCode, err)
		}
	}
	return res.StatusCode
}

// expect fails the test unless the request returns the wanted status.
func (c *client) expect(want int, method, path string, body, out any) {
	c.t.Helper()
	if got := c.do(method, path, body, out); got != want {
		c.t.Fatalf("%s %s: status %d, want %d", method, path, got, want)
	}
}

var tokenPattern = regexp.MustCompile(`#token=([A-Za-z0-9_-]+)`)

// signIn completes the magic-link flow and returns the signed-in client.
func (e *testEnv) signIn(t *testing.T, email string) (*client, api.Me) {
	t.Helper()
	c := e.client(t)
	c.expect(http.StatusNoContent, "POST", "/api/auth/magic-link", api.MagicLinkRequest{Email: email}, nil)
	// Mail goes to the normalized address.
	match := tokenPattern.FindStringSubmatch(e.mail.lastTo(t, strings.ToLower(email)).Text)
	if match == nil {
		t.Fatal("magic-link email has no token")
	}
	var me api.Me
	c.expect(http.StatusOK, "POST", "/api/auth/verify", api.VerifyRequest{Token: match[1]}, &me)
	return c, me
}

func (c *client) createWorkspace(name, urlKey string) api.Workspace {
	c.t.Helper()
	var ws api.Workspace
	c.expect(http.StatusCreated, "POST", "/api/workspaces", api.CreateWorkspaceRequest{Name: name, UrlKey: urlKey}, &ws)
	return ws
}

func TestMagicLinkSignIn(t *testing.T) {
	env := newTestEnv(t)

	anon := env.client(t)
	anon.expect(http.StatusUnauthorized, "GET", "/api/me", nil, nil)
	anon.expect(http.StatusBadRequest, "POST", "/api/auth/magic-link", api.MagicLinkRequest{Email: "not-an-email"}, nil)
	anon.expect(http.StatusBadRequest, "POST", "/api/auth/verify", api.VerifyRequest{Token: "bogus"}, nil)

	c, me := env.signIn(t, "Ada@Example.com")
	if me.User.Email != "ada@example.com" {
		t.Errorf("email = %q, want normalized ada@example.com", me.User.Email)
	}
	if len(me.Workspaces) != 0 {
		t.Errorf("new user has %d workspaces, want 0", len(me.Workspaces))
	}

	// The link is single-use.
	token := tokenPattern.FindStringSubmatch(env.mail.lastTo(t, "ada@example.com").Text)[1]
	anon.expect(http.StatusBadRequest, "POST", "/api/auth/verify", api.VerifyRequest{Token: token}, nil)

	var user api.User
	c.expect(http.StatusOK, "PATCH", "/api/me", map[string]any{"name": "  Ada Lovelace "}, &user)
	if user.Name != "Ada Lovelace" {
		t.Errorf("name = %q, want trimmed", user.Name)
	}
	c.expect(http.StatusBadRequest, "PATCH", "/api/me", map[string]any{"name": "   "}, nil)

	// Signing in again reuses the account.
	_, again := env.signIn(t, "ada@example.com")
	if again.User.Id != me.User.Id || again.User.Name != "Ada Lovelace" {
		t.Errorf("second sign-in returned a different user: %+v", again.User)
	}

	c.expect(http.StatusNoContent, "POST", "/api/auth/logout", nil, nil)
	c.expect(http.StatusUnauthorized, "GET", "/api/me", nil, nil)
}

func TestMagicLinkRateLimitPerEmail(t *testing.T) {
	env := newTestEnv(t)
	c := env.client(t)
	req := api.MagicLinkRequest{Email: "ada@example.com"}
	for range loginTokenLimit {
		c.expect(http.StatusNoContent, "POST", "/api/auth/magic-link", req, nil)
	}
	c.expect(http.StatusTooManyRequests, "POST", "/api/auth/magic-link", req, nil)
}

func TestCrossOriginRequestsRejected(t *testing.T) {
	env := newTestEnv(t)
	c, _ := env.signIn(t, "ada@example.com")

	body := api.CreateWorkspaceRequest{Name: "Evil", UrlKey: "evil"}
	if got := c.do("POST", "/api/workspaces", body, nil, "Sec-Fetch-Site", "cross-site"); got != http.StatusForbidden {
		t.Errorf("cross-site POST: status %d, want 403", got)
	}
	if got := c.do("POST", "/api/workspaces", body, nil, "Origin", "http://evil.test"); got != http.StatusForbidden {
		t.Errorf("foreign-origin POST: status %d, want 403", got)
	}
	var ws api.Workspace
	if got := c.do("POST", "/api/workspaces", body, &ws, "Origin", "http://app.test"); got != http.StatusCreated {
		t.Errorf("same-origin POST: status %d, want 201", got)
	}
}

func TestWorkspacesAndTeams(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")

	ada.expect(http.StatusBadRequest, "POST", "/api/workspaces", api.CreateWorkspaceRequest{Name: "Acme", UrlKey: "settings"}, nil)
	ada.expect(http.StatusBadRequest, "POST", "/api/workspaces", api.CreateWorkspaceRequest{Name: "Acme", UrlKey: "-bad-"}, nil)

	ws := ada.createWorkspace("Acme Inc", "acme")
	if ws.Role != api.WorkspaceRoleAdmin {
		t.Errorf("creator role = %q, want admin", ws.Role)
	}
	ada.expect(http.StatusConflict, "POST", "/api/workspaces", api.CreateWorkspaceRequest{Name: "Other", UrlKey: "acme"}, nil)

	var me api.Me
	ada.expect(http.StatusOK, "GET", "/api/me", nil, &me)
	if len(me.Workspaces) != 1 || me.Workspaces[0].UrlKey != "acme" {
		t.Fatalf("workspaces = %+v, want [acme]", me.Workspaces)
	}

	// A first team comes with the workspace.
	teamsPath := "/api/workspaces/" + ws.Id.String() + "/teams"
	var teams []api.Team
	ada.expect(http.StatusOK, "GET", teamsPath, nil, &teams)
	if len(teams) != 1 || teams[0].Key != "ACM" || !teams[0].IsMember {
		t.Fatalf("teams = %+v, want one team ACM with membership", teams)
	}

	var eng api.Team
	ada.expect(http.StatusCreated, "POST", teamsPath, api.CreateTeamRequest{Name: "Engineering", Key: "eng"}, &eng)
	if eng.Key != "ENG" {
		t.Errorf("team key = %q, want uppercased ENG", eng.Key)
	}
	ada.expect(http.StatusConflict, "POST", teamsPath, api.CreateTeamRequest{Name: "Engines", Key: "ENG"}, nil)
	ada.expect(http.StatusBadRequest, "POST", teamsPath, api.CreateTeamRequest{Name: "Bad", Key: "1X"}, nil)

	// Outsiders can't tell the workspace or its teams exist.
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", teamsPath, nil, nil)
	eve.expect(http.StatusNotFound, "POST", teamsPath, api.CreateTeamRequest{Name: "Sneaky", Key: "SNK"}, nil)
	eve.expect(http.StatusNotFound, "GET", "/api/workspaces/"+ws.Id.String()+"/members", nil, nil)
	eve.expect(http.StatusNotFound, "GET", "/api/teams/"+eng.Id.String()+"/members", nil, nil)
	eve.expect(http.StatusNotFound, "PUT", "/api/teams/"+eng.Id.String()+"/members/"+me.User.Id.String(), nil, nil)
}

func TestInvitesAndTeamMembership(t *testing.T) {
	env := newTestEnv(t)
	ada, adaMe := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	base := "/api/workspaces/" + ws.Id.String()

	member := api.WorkspaceRoleMember
	var invite api.WorkspaceInvite
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "Bob@Example.com", Role: &member}, &invite)
	ada.expect(http.StatusConflict, "POST", base+"/invites", api.CreateInviteRequest{Email: "bob@example.com"}, nil)
	ada.expect(http.StatusConflict, "POST", base+"/invites", api.CreateInviteRequest{Email: "ada@example.com"}, nil)

	// A revoked invite grants nothing.
	var revoked api.WorkspaceInvite
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "carol@example.com"}, &revoked)
	ada.expect(http.StatusNoContent, "DELETE", base+"/invites/"+revoked.Id.String(), nil, nil)
	ada.expect(http.StatusNotFound, "DELETE", base+"/invites/"+revoked.Id.String(), nil, nil)
	_, carolMe := env.signIn(t, "carol@example.com")
	if len(carolMe.Workspaces) != 0 {
		t.Errorf("revoked invitee joined %d workspaces", len(carolMe.Workspaces))
	}

	// Signing in with the invited address joins the workspace.
	bob, bobMe := env.signIn(t, "bob@example.com")
	if len(bobMe.Workspaces) != 1 || bobMe.Workspaces[0].Role != api.WorkspaceRoleMember {
		t.Fatalf("bob's workspaces = %+v, want acme as member", bobMe.Workspaces)
	}
	var invites []api.WorkspaceInvite
	ada.expect(http.StatusOK, "GET", base+"/invites", nil, &invites)
	if len(invites) != 0 {
		t.Errorf("%d invites still pending after acceptance", len(invites))
	}
	var members []api.WorkspaceMember
	bob.expect(http.StatusOK, "GET", base+"/members", nil, &members)
	if len(members) != 2 {
		t.Errorf("%d members, want 2", len(members))
	}

	// Only admins manage invites.
	bob.expect(http.StatusForbidden, "POST", base+"/invites", api.CreateInviteRequest{Email: "dan@example.com"}, nil)
	bob.expect(http.StatusForbidden, "GET", base+"/invites", nil, nil)

	// Bob sees the public team, joins it, and leaves again.
	var teams []api.Team
	bob.expect(http.StatusOK, "GET", base+"/teams", nil, &teams)
	if len(teams) != 1 || teams[0].IsMember {
		t.Fatalf("bob's teams = %+v, want one team he hasn't joined", teams)
	}
	bobInTeam := "/api/teams/" + teams[0].Id.String() + "/members/" + bobMe.User.Id.String()
	adaInTeam := "/api/teams/" + teams[0].Id.String() + "/members/" + adaMe.User.Id.String()
	bob.expect(http.StatusNoContent, "PUT", bobInTeam, nil, nil)
	var teamMembers []api.User
	bob.expect(http.StatusOK, "GET", "/api/teams/"+teams[0].Id.String()+"/members", nil, &teamMembers)
	if len(teamMembers) != 2 {
		t.Errorf("%d team members, want 2", len(teamMembers))
	}
	bob.expect(http.StatusForbidden, "DELETE", adaInTeam, nil, nil) // can't remove others
	bob.expect(http.StatusNoContent, "DELETE", bobInTeam, nil, nil)

	// Private teams are invisible to non-members.
	private := true
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)
	bob.expect(http.StatusOK, "GET", base+"/teams", nil, &teams)
	if len(teams) != 1 {
		t.Errorf("bob sees %d teams, want only the public one", len(teams))
	}
	bobInSecret := "/api/teams/" + secret.Id.String() + "/members/" + bobMe.User.Id.String()
	bob.expect(http.StatusNotFound, "GET", "/api/teams/"+secret.Id.String()+"/members", nil, nil)
	bob.expect(http.StatusNotFound, "PUT", bobInSecret, nil, nil)
	ada.expect(http.StatusNoContent, "PUT", bobInSecret, nil, nil)
	bob.expect(http.StatusOK, "GET", base+"/teams", nil, &teams)
	if len(teams) != 2 {
		t.Errorf("bob sees %d teams after being added, want 2", len(teams))
	}
}

func TestGuestsOnlySeeTheirTeams(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	base := "/api/workspaces/" + ws.Id.String()

	guest := api.WorkspaceRoleGuest
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "gus@example.com", Role: &guest}, nil)
	gus, gusMe := env.signIn(t, "gus@example.com")

	var teams []api.Team
	gus.expect(http.StatusOK, "GET", base+"/teams", nil, &teams)
	if len(teams) != 0 {
		t.Fatalf("guest sees %d teams before being added to any", len(teams))
	}
	gus.expect(http.StatusForbidden, "POST", base+"/teams", api.CreateTeamRequest{Name: "Mine", Key: "MINE"}, nil)

	ada.expect(http.StatusOK, "GET", base+"/teams", nil, &teams)
	gusInTeam := "/api/teams/" + teams[0].Id.String() + "/members/" + gusMe.User.Id.String()
	gus.expect(http.StatusNotFound, "PUT", gusInTeam, nil, nil) // can't self-join
	ada.expect(http.StatusNoContent, "PUT", gusInTeam, nil, nil)
	gus.expect(http.StatusOK, "GET", base+"/teams", nil, &teams)
	if len(teams) != 1 {
		t.Errorf("guest sees %d teams after being added, want 1", len(teams))
	}
}

func TestContentSecurityPolicyCloudflareAnalytics(t *testing.T) {
	s := &Server{cfg: config.Config{AppURL: "https://app.test"}}
	if csp := s.contentSecurityPolicy(); strings.Contains(csp, "cloudflareinsights") || !strings.Contains(csp, "script-src 'self';") {
		t.Errorf("policy without the setting = %q", csp)
	}
	s.cfg.CloudflareAnalytics = true
	csp := s.contentSecurityPolicy()
	for _, want := range []string{
		"script-src 'self' https://static.cloudflareinsights.com;",
		"connect-src 'self' ws://app.test wss://app.test https://cloudflareinsights.com;",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy = %q, want it to contain %q", csp, want)
		}
	}
}
