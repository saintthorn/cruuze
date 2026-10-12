package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/importer"
)

// Connecting a tracker: instead of pasting a token, an admin is sent to the
// tracker's own sign-in page (OAuth) and comes back with one, which is kept
// encrypted and is theirs alone. It needs an application registered with
// the tracker (the IMPORT_*_CLIENT_ID settings) and a SECRETS_KEY.

const (
	importConnectCookie = "cruuze_import_connect"
	importConnectPath   = "/api/imports/connect"
	// maxConnectedRepos bounds the list of repositories offered for a
	// GitHub connection.
	maxConnectedRepos = 300
)

// importProvider is a tracker that can be connected to, and the OAuth
// application this server is registered as there.
type importProvider struct {
	name                   string // as in the API: "github"
	title                  string // as people say it: "GitHub"
	clientID, clientSecret string
	authURL, tokenURL      string
	scope                  string
	// extra are parameters the tracker's sign-in page wants besides the usual.
	extra url.Values
}

// importProviders are the trackers this server is set up to connect to.
func (s *Server) importProviders() []importProvider {
	if s.sealKey == nil {
		return nil
	}
	var out []importProvider
	if id := s.cfg.ImportGitHubClientID; id != "" {
		out = append(out, importProvider{
			name: "github", title: "GitHub", clientID: id, clientSecret: s.cfg.ImportGitHubClientSecret,
			authURL: "https://github.com/login/oauth/authorize", tokenURL: "https://github.com/login/oauth/access_token",
			// Reading the issues of private repositories has no narrower scope.
			scope: "repo",
		})
	}
	if id := s.cfg.ImportAtlassianClientID; id != "" {
		out = append(out, importProvider{
			name: "jira", title: "Jira", clientID: id, clientSecret: s.cfg.ImportAtlassianClientSecret,
			authURL: "https://auth.atlassian.com/authorize", tokenURL: "https://auth.atlassian.com/oauth/token",
			// offline_access brings a refresh token: access lasts an hour.
			scope: "read:jira-work read:jira-user offline_access",
			extra: url.Values{"audience": {"api.atlassian.com"}, "prompt": {"consent"}},
		})
	}
	if id := s.cfg.ImportLinearClientID; id != "" {
		out = append(out, importProvider{
			name: "linear", title: "Linear", clientID: id, clientSecret: s.cfg.ImportLinearClientSecret,
			authURL: "https://linear.app/oauth/authorize", tokenURL: "https://api.linear.app/oauth/token",
			scope: "read",
		})
	}
	return out
}

func (s *Server) importProvider(name string) (importProvider, bool) {
	for _, p := range s.importProviders() {
		if p.name == name {
			return p, true
		}
	}
	return importProvider{}, false
}

func (s *Server) importRedirectURL(p importProvider) string {
	return s.cfg.AppURL + importConnectPath + "/" + p.name + "/callback"
}

// connectionSecret is what is kept, encrypted, of a connection.
type connectionSecret struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh,omitempty"`
	// Expires is when Access stops working; zero if it doesn't.
	Expires time.Time `json:"expires,omitzero"`
	// For Jira: Atlassian's ID of the site, and the site's address.
	CloudID string `json:"cloudId,omitempty"`
	Site    string `json:"site,omitempty"`
}

func connectionBound(workspaceID, userID uuid.UUID, provider string) string {
	return "connection:" + workspaceID.String() + ":" + userID.String() + ":" + provider
}

// connectImportStart sends an admin to a tracker's sign-in page.
func (s *Server) connectImportStart(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := uuid.Parse(chi.URLParam(r, "workspaceId"))
	provider, found := s.importProvider(chi.URLParam(r, "provider"))
	if err != nil || !found {
		notFound(w)
		return
	}
	if !s.requireBrowser(w, r) || !s.requireWorkspaceAdmin(w, r, workspaceID) {
		return
	}
	// The cookie ties the answer to this browser and says which workspace
	// the connection is for.
	state := auth.NewToken()
	http.SetCookie(w, &http.Cookie{
		Name:     importConnectCookie,
		Value:    state + "." + workspaceID.String(),
		Path:     importConnectPath,
		MaxAge:   600,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	query := url.Values{
		"client_id":     {provider.clientID},
		"redirect_uri":  {s.importRedirectURL(provider)},
		"response_type": {"code"},
		"scope":         {provider.scope},
		"state":         {state},
	}
	for name, values := range provider.extra {
		query[name] = values
	}
	http.Redirect(w, r, provider.authURL+"?"+query.Encode(), http.StatusFound)
}

// connectImportCallback is where the tracker sends the admin back: it turns
// the code they bring into tokens, keeps those, and returns to the app.
func (s *Server) connectImportCallback(w http.ResponseWriter, r *http.Request) {
	provider, found := s.importProvider(chi.URLParam(r, "provider"))
	if !found {
		notFound(w)
		return
	}
	// Back to the import page of the workspace, saying how it went.
	back := func(urlKey, outcome string) {
		target := s.cfg.AppURL
		if urlKey != "" {
			target += "/" + urlKey + "/settings/import"
		}
		http.Redirect(w, r, target+"?"+outcome+"="+provider.name, http.StatusFound)
	}
	fail := func(urlKey, reason string, err error) {
		s.log.Warn("connecting a tracker failed", "provider", provider.name, "reason", reason, "err", err)
		back(urlKey, "connect_error")
	}

	c, err := r.Cookie(importConnectCookie)
	if err != nil {
		fail("", "missing state cookie", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: importConnectCookie, Path: importConnectPath, MaxAge: -1})
	state, rawWorkspace, ok := strings.Cut(c.Value, ".")
	workspaceID, err := uuid.Parse(rawWorkspace)
	if !ok || err != nil || subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
		fail("", "state mismatch", err)
		return
	}
	ctx := r.Context()
	sess := currentSession(r)
	workspace, err := s.q.GetWorkspace(ctx, workspaceID)
	if err != nil {
		fail("", "no such workspace", err)
		return
	}
	role, err := s.q.GetWorkspaceRole(ctx, dbgen.GetWorkspaceRoleParams{WorkspaceID: workspaceID, UserID: sess.User.ID})
	if err != nil || role != roleAdmin || sess.keyWorkspace != nil {
		fail("", "not an admin of the workspace", err)
		return
	}
	if denied := r.URL.Query().Get("error"); denied != "" {
		fail(workspace.UrlKey, "refused at the tracker: "+denied, nil)
		return
	}

	secret, err := s.exchangeImportToken(ctx, provider, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {r.URL.Query().Get("code")},
		"redirect_uri": {s.importRedirectURL(provider)},
	})
	if err != nil {
		fail(workspace.UrlKey, "exchanging the code", err)
		return
	}
	label, err := s.describeConnection(ctx, provider, &secret)
	if err != nil {
		fail(workspace.UrlKey, "asking whose account it is", err)
		return
	}
	sealed, err := s.sealConnection(workspaceID, sess.User.ID, provider.name, secret)
	if err != nil {
		fail(workspace.UrlKey, "sealing the tokens", err)
		return
	}
	_, err = s.q.UpsertImportConnection(ctx, dbgen.UpsertImportConnectionParams{
		ID: newID(), WorkspaceID: workspaceID, UserID: sess.User.ID, Provider: provider.name, Label: truncate(label, 200), Secret: sealed,
	})
	if err != nil {
		fail(workspace.UrlKey, "saving the connection", err)
		return
	}
	back(workspace.UrlKey, "connected")
}

func (s *Server) sealConnection(workspaceID, userID uuid.UUID, provider string, secret connectionSecret) ([]byte, error) {
	plain, err := json.Marshal(secret)
	if err != nil {
		return nil, err
	}
	return s.sealKey.Seal(plain, connectionBound(workspaceID, userID, provider))
}

// exchangeImportToken asks a tracker's token endpoint for tokens: for the
// code an admin came back with, or for a refresh token.
func (s *Server) exchangeImportToken(ctx context.Context, p importProvider, form url.Values) (connectionSecret, error) {
	var secret connectionSecret
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return secret, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var answer struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	if err := s.importJSON(req, &answer); err != nil {
		return secret, err
	}
	if answer.AccessToken == "" {
		return secret, fmt.Errorf("%s gave no token: %s %s", p.title, answer.Error, truncate(answer.Description, 200))
	}
	secret.Access, secret.Refresh = answer.AccessToken, answer.RefreshToken
	if answer.ExpiresIn > 0 {
		secret.Expires = time.Now().Add(time.Duration(answer.ExpiresIn) * time.Second)
	}
	return secret, nil
}

// importJSON sends a request to a tracker and decodes its JSON answer.
func (s *Server) importJSON(req *http.Request, out any) error {
	res, err := s.importHTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	// GitHub answers a refused code with 200 and an error in the body, so
	// the body is read either way; out says whether it held what was wanted.
	if jsonErr := json.Unmarshal(data, out); jsonErr != nil || res.StatusCode >= 500 {
		return fmt.Errorf("%s answered %d: %s", req.URL.Host, res.StatusCode, truncate(strings.TrimSpace(string(data)), 200))
	}
	if res.StatusCode >= 400 && res.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("%s answered %d", req.URL.Host, res.StatusCode)
	}
	return nil
}

func (s *Server) importGet(ctx context.Context, address, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cruuze-importer")
	return s.importJSON(req, out)
}

// describeConnection asks a tracker whose account the tokens are for, to
// name the connection. For Jira it also learns which site they reach.
func (s *Server) describeConnection(ctx context.Context, p importProvider, secret *connectionSecret) (string, error) {
	switch p.name {
	case "github":
		var user struct {
			Login string `json:"login"`
		}
		if err := s.importGet(ctx, "https://api.github.com/user", secret.Access, &user); err != nil {
			return "", err
		}
		if user.Login == "" {
			return "", errors.New("GitHub didn't say whose account it is")
		}
		return user.Login, nil
	case "linear":
		body := strings.NewReader(`{"query":"{ organization { name } viewer { name } }"}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.linear.app/graphql", body)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+secret.Access)
		req.Header.Set("Content-Type", "application/json")
		var out struct {
			Data struct {
				Organization struct {
					Name string `json:"name"`
				} `json:"organization"`
				Viewer struct {
					Name string `json:"name"`
				} `json:"viewer"`
			} `json:"data"`
		}
		if err := s.importJSON(req, &out); err != nil {
			return "", err
		}
		if out.Data.Organization.Name == "" && out.Data.Viewer.Name == "" {
			return "", errors.New("Linear didn't say whose account it is")
		}
		return cmpOr(out.Data.Organization.Name, out.Data.Viewer.Name), nil
	case "jira":
		var sites []struct {
			ID     string   `json:"id"`
			URL    string   `json:"url"`
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if err := s.importGet(ctx, "https://api.atlassian.com/oauth/token/accessible-resources", secret.Access, &sites); err != nil {
			return "", err
		}
		// The account may have been given access to several sites and
		// products; the first that is a Jira is the one.
		for _, site := range sites {
			if site.ID == "" || site.URL == "" || !slices.ContainsFunc(site.Scopes, func(scope string) bool { return strings.Contains(scope, "jira") }) {
				continue
			}
			secret.CloudID, secret.Site = site.ID, strings.TrimRight(site.URL, "/")
			if u, err := url.Parse(site.URL); err == nil && u.Host != "" {
				return u.Host, nil
			}
			return site.Name, nil
		}
		return "", errors.New("the Atlassian account was given access to no Jira site")
	}
	return "", fmt.Errorf("no such provider: %s", p.name)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// importConnection loads the connection a person has made to a tracker.
func (s *Server) importConnection(ctx context.Context, workspaceID, userID uuid.UUID, provider string) (dbgen.ImportConnection, connectionSecret, error) {
	var secret connectionSecret
	if s.sealKey == nil {
		return dbgen.ImportConnection{}, secret, invalidImport("This server isn't set up for connections. Use a token.")
	}
	conn, err := s.q.GetImportConnection(ctx, dbgen.GetImportConnectionParams{WorkspaceID: workspaceID, UserID: userID, Provider: provider})
	if db.IsNoRows(err) {
		return conn, secret, invalidImport("Connect the account first, or use a token.")
	}
	if err != nil {
		return conn, secret, err
	}
	plain, err := s.sealKey.Open(conn.Secret, connectionBound(workspaceID, userID, provider))
	if err != nil {
		// Sealed with another key: the server's was changed.
		return conn, secret, invalidImport("The connection can no longer be read. Connect again.")
	}
	return conn, secret, json.Unmarshal(plain, &secret)
}

// connectionToken gives a connection's access token to an importer, getting
// a newer one when it is about to run out or has been refused.
func (s *Server) connectionToken(conn dbgen.ImportConnection, secret connectionSecret) importer.TokenFunc {
	var mu sync.Mutex
	var renewedAt time.Time
	return func(ctx context.Context, used string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		refused := used != "" && used == secret.Access
		expiring := !secret.Expires.IsZero() && time.Until(secret.Expires) < 2*time.Minute
		// A token just renewed and refused again isn't out of date: it is
		// not allowed whatever was asked, and another wouldn't be either.
		if (!refused && !expiring) || (refused && time.Since(renewedAt) < time.Minute) {
			return secret.Access, nil
		}
		lapsed := &importer.UserError{Message: "The connection has lapsed. Connect the account again."}
		provider, found := s.importProvider(conn.Provider)
		if secret.Refresh == "" || !found {
			if refused {
				return "", lapsed
			}
			return secret.Access, nil
		}
		fresh, err := s.exchangeImportToken(ctx, provider, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {secret.Refresh}})
		if err != nil {
			s.log.Warn("refreshing a connection failed", "provider", conn.Provider, "err", err)
			if refused || time.Now().After(secret.Expires) {
				return "", lapsed
			}
			return secret.Access, nil
		}
		// Some trackers give a new refresh token each time, some keep the old.
		fresh.Refresh = cmpOr(fresh.Refresh, secret.Refresh)
		fresh.CloudID, fresh.Site = secret.CloudID, secret.Site
		secret, renewedAt = fresh, time.Now()
		sealed, err := s.sealConnection(conn.WorkspaceID, conn.UserID, conn.Provider, secret)
		if err == nil {
			err = s.q.SetImportConnectionSecret(ctx, dbgen.SetImportConnectionSecretParams{ID: conn.ID, Secret: sealed})
		}
		if err != nil {
			// The import goes on with the token it has; the next one may
			// have to connect again.
			s.log.Error("saving refreshed connection", "provider", conn.Provider, "err", err)
		}
		return secret.Access, nil
	}
}

// connectedSource builds the source of an import that is read through the
// connection userID has made to the tracker.
func (s *Server) connectedSource(ctx context.Context, workspaceID, userID uuid.UUID, req api.ImportRequest, teams []string) (importer.Source, error) {
	if !resumableSource(req.Source) {
		return nil, invalidImport("Only GitHub, Linear and Jira can be read through a connection.")
	}
	conn, secret, err := s.importConnection(ctx, workspaceID, userID, string(req.Source))
	if err != nil {
		return nil, err
	}
	token := s.connectionToken(conn, secret)
	switch req.Source {
	case api.ImportSourceGithub:
		if req.Github == nil {
			return nil, invalidImport("Say which repository to import.")
		}
		// GitHub's tokens for OAuth applications don't run out.
		return importer.NewGitHub(s.importHTTP, req.Github.Repo, secret.Access, "")
	case api.ImportSourceLinear:
		return importer.NewLinearConnected(s.importHTTP, token, teams), nil
	default:
		return importer.NewJiraConnected(s.importHTTP, secret.CloudID, secret.Site, token, teams)
	}
}

func (s *Server) ListImportConnections(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireBrowser(w, r) || !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	out := api.ImportConnections{
		Available:   []api.ImportProvider{},
		Connections: []api.ImportConnection{},
		Resumable:   s.sealKey != nil,
	}
	for _, p := range s.importProviders() {
		out.Available = append(out.Available, api.ImportProvider(p.name))
	}
	rows, err := s.q.ListImportConnections(r.Context(), dbgen.ListImportConnectionsParams{WorkspaceID: workspaceId, UserID: currentSession(r).User.ID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, row := range rows {
		// One for a tracker the server is no longer set up for can't be used.
		if slices.Contains(out.Available, api.ImportProvider(row.Provider)) {
			out.Connections = append(out.Connections, api.ImportConnection{Provider: api.ImportProvider(row.Provider), Label: row.Label, ConnectedAt: row.UpdatedAt})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) DeleteImportConnection(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId, provider api.ImportProvider) {
	if !s.requireBrowser(w, r) || !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	_, err := s.q.DeleteImportConnection(r.Context(), dbgen.DeleteImportConnectionParams{WorkspaceID: workspaceId, UserID: currentSession(r).User.ID, Provider: string(provider)})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ListImportConnectionRepos(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireBrowser(w, r) || !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	ctx := r.Context()
	_, secret, err := s.importConnection(ctx, workspaceId, currentSession(r).User.ID, "github")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	repos := []string{}
	for page := 1; len(repos) < maxConnectedRepos; page++ {
		var batch []struct {
			FullName  string `json:"full_name"`
			HasIssues bool   `json:"has_issues"`
		}
		address := fmt.Sprintf("https://api.github.com/user/repos?per_page=100&sort=pushed&page=%d", page)
		if err := s.importGet(ctx, address, secret.Access, &batch); err != nil {
			s.log.Warn("listing a connection's repositories", "err", err)
			writeError(w, http.StatusBadGateway, "source_unavailable", "GitHub didn't list the repositories. Connect again, or try later.")
			return
		}
		for _, repo := range batch {
			if repo.HasIssues && repo.FullName != "" {
				repos = append(repos, repo.FullName)
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	writeJSON(w, http.StatusOK, repos)
}
