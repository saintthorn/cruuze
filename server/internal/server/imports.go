package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/importer"
)

const (
	// maxImportRequestBytes bounds an import request, which may carry a
	// whole file of issues.
	maxImportRequestBytes = 64 << 20
	importPreviewTimeout  = 60 * time.Second
	importProgressEvery   = 3 * time.Second
)

// newImportClient returns the HTTP client importers read other trackers
// with. Like webhooks, the addresses are supplied by users, so unless
// allowPrivate is set it refuses to connect anywhere that isn't on the
// public internet. Redirects are followed (files are served from other
// hosts); the check is on each address actually dialed.
func newImportClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			addrPort, err := netip.ParseAddrPort(address)
			if err != nil || !isPublicAddr(addrPort.Addr()) {
				return errPrivateAddress
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
		},
	}
}

func importRecord(i dbgen.Import) api.Import {
	counts := map[string]int{}
	_ = json.Unmarshal(i.Counts, &counts)
	warnings := i.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	out := api.Import{
		Id: i.ID, Source: i.Source, Label: i.Label, Status: api.ImportStatus(i.Status), Stage: i.Stage,
		Counts: counts, Warnings: warnings, Error: i.Error,
		CreatedBy: i.CreatedBy, CreatedAt: i.CreatedAt, FinishedAt: i.FinishedAt,
	}
	request := map[string]any{}
	if err := json.Unmarshal(i.Request, &request); err == nil && len(request) > 0 {
		out.Request = &request
	}
	return out
}

// rerunnableRequest is what to keep of an import's request so that it can be
// run again: everything but the credentials, which whoever runs it again
// gives anew. An import from a file can't be rerun without the file, and
// nothing is kept for it.
func rerunnableRequest(req api.ImportRequest) []byte {
	// Through a connection there are no settings to speak of, and no
	// credentials among them.
	connected := req.UseConnection != nil && *req.UseConnection
	switch req.Source {
	case api.ImportSourceGithub:
		if req.Github == nil {
			return []byte("{}")
		}
		github := *req.Github
		github.Token = nil
		req.Github = &github
	case api.ImportSourceLinear:
		if req.Linear != nil {
			linear := *req.Linear
			linear.ApiKey = nil
			req.Linear = &linear
		} else if !connected {
			return []byte("{}")
		}
	case api.ImportSourceJira:
		if req.Jira != nil {
			jira := *req.Jira
			jira.Token = nil
			req.Jira = &jira
		} else if !connected {
			return []byte("{}")
		}
	default:
		return []byte("{}")
	}
	req.Csv, req.Bundle = nil, nil
	encoded, err := json.Marshal(req)
	if err != nil {
		return []byte("{}")
	}
	return encoded
}

// decodeImportRequest reads an import request, which may be far larger than
// other requests.
func decodeImportRequest(w http.ResponseWriter, r *http.Request) (api.ImportRequest, bool) {
	var req api.ImportRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxImportRequestBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That file is too large to import in one go (64 MB at most). Split it by team.")
		} else {
			writeError(w, http.StatusBadRequest, "bad_request", "Request body is not valid JSON.")
		}
		return req, false
	}
	return req, true
}

func invalidImport(message string) *apiError {
	return &apiError{http.StatusBadRequest, "invalid_import", message}
}

// importSource builds the source an import request names, read with the
// request's own credentials or, if it says so, through the connection that
// userID has made to that tracker.
func (s *Server) importSource(ctx context.Context, workspaceID, userID uuid.UUID, req api.ImportRequest) (importer.Source, error) {
	var teams []string
	if req.Teams != nil {
		teams = *req.Teams
	}
	if req.UseConnection != nil && *req.UseConnection {
		return s.connectedSource(ctx, workspaceID, userID, req, teams)
	}
	text := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	missing := func(what string) error {
		return invalidImport(`Say what to import from: the request has no "` + what + `" settings.`)
	}
	switch req.Source {
	case api.ImportSourceBundle:
		if req.Bundle == nil {
			return nil, missing("bundle")
		}
		// Through JSON again: the file's shape is the importer's to define.
		raw, err := json.Marshal(req.Bundle)
		if err != nil {
			return nil, err
		}
		var file importer.File
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, invalidImport("The file isn't in the import format: " + err.Error())
		}
		return importer.FromFile(file, teams)
	case api.ImportSourceCsv:
		if req.Csv == nil {
			return nil, missing("csv")
		}
		name, teamName := "", ""
		if req.Csv.Name != nil {
			name = *req.Csv.Name
		}
		if req.Csv.TeamName != nil {
			teamName = *req.Csv.TeamName
		}
		source, err := importer.FromCSV(req.Csv.Text, name, teamName)
		if err != nil {
			return nil, err
		}
		return importer.OnlyTeams(source, teams), nil
	case api.ImportSourceGithub:
		if req.Github == nil {
			return nil, missing("github")
		}
		token, base := "", ""
		if req.Github.Token != nil {
			token = *req.Github.Token
		}
		if req.Github.BaseUrl != nil {
			base = *req.Github.BaseUrl
		}
		return importer.NewGitHub(s.importHTTP, req.Github.Repo, token, base)
	case api.ImportSourceLinear:
		if req.Linear == nil {
			return nil, missing("linear")
		}
		return importer.NewLinear(s.importHTTP, text(req.Linear.ApiKey), teams)
	case api.ImportSourceJira:
		if req.Jira == nil {
			return nil, missing("jira")
		}
		email := ""
		if req.Jira.Email != nil {
			email = *req.Jira.Email
		}
		return importer.NewJira(s.importHTTP, text(req.Jira.BaseUrl), email, text(req.Jira.Token), teams)
	default:
		return nil, invalidImport(fmt.Sprintf("There is no importer for %q.", req.Source))
	}
}

// importMessage is what to tell the person about an importer's failure.
// Whoever runs an import gave the credentials, so they are shown what went
// wrong; credentials themselves travel in headers and are never in it.
func importMessage(err error) string {
	var userErr *importer.UserError
	switch {
	case errors.As(err, &userErr):
		return userErr.Message
	case errors.Is(err, errPrivateAddress):
		return "That address is on a private network, which this server doesn't import from. Whoever runs the server can allow it with IMPORT_ALLOW_PRIVATE=true."
	case errors.Is(err, context.DeadlineExceeded):
		return "The source took too long to answer. Try again."
	case errors.Is(err, context.Canceled):
		return "The server stopped while this import was running. Run it again: what was already imported is not duplicated."
	}
	return ""
}

func (s *Server) failImport(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		s.fail(w, r, err)
		return
	}
	if message := importMessage(err); message != "" {
		writeError(w, http.StatusBadRequest, "import_failed", message)
		return
	}
	s.log.Warn("import: source failed", "err", err)
	writeError(w, http.StatusBadGateway, "import_failed", "Couldn't read from the source: "+truncate(err.Error(), 300))
}

func (s *Server) PreviewImport(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	req, ok := decodeImportRequest(w, r)
	if !ok {
		return
	}
	source, err := s.importSource(r.Context(), workspaceId, currentSession(r).User.ID, req)
	if err != nil {
		s.failImport(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), importPreviewTimeout)
	defer cancel()
	preview, err := source.Preview(ctx)
	if err != nil {
		s.failImport(w, r, err)
		return
	}

	// Only teams the admin can see are offered, as the import itself does.
	seen, err := s.q.ListTeamsForUser(ctx, dbgen.ListTeamsForUserParams{WorkspaceID: workspaceId, UserID: currentSession(r).User.ID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	teams := make([]dbgen.Team, 0, len(seen))
	for _, t := range seen {
		teams = append(teams, t.Team)
	}
	members, err := s.q.ListWorkspaceMembers(ctx, workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Where each team went the last time this origin was imported.
	before, err := s.q.ListImportItems(ctx, dbgen.ListImportItemsParams{WorkspaceID: workspaceId, Origin: preview.Origin, Kind: "team"})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	people := importer.NewPeople(currentSession(r).User.ID)
	for _, m := range members {
		people.Add(m.User.ID, m.User.Email, m.User.Name, m.User.GithubLogin)
	}
	out := api.ImportPreview{Label: preview.Label, Teams: []api.ImportPreviewTeam{}, Users: []api.ImportPreviewUser{}}
	for _, t := range preview.Teams {
		entry := api.ImportPreviewTeam{Id: t.ID, Key: t.Key, Name: t.Name, Issues: int32(t.Issues)}
		for _, existing := range teams {
			if strings.EqualFold(existing.Key, t.Key) {
				entry.TeamId = &existing.ID
			}
		}
		for _, b := range before {
			if b.ExternalID == t.ID && slicesContainTeam(teams, b.RecordID) {
				entry.TeamId = &b.RecordID
			}
		}
		out.Teams = append(out.Teams, entry)
	}
	for _, u := range preview.Users {
		entry := api.ImportPreviewUser{Id: u.ID, Name: u.Name}
		if entry.Name == "" {
			entry.Name = u.Email
		}
		if u.Email != "" {
			entry.Email = &u.Email
		}
		// The same matching the import itself will do.
		entry.UserId = people.Match(u)
		out.Users = append(out.Users, entry)
	}
	writeJSON(w, http.StatusOK, out)
}

// importSinceMargin is how far before the last import an incremental one
// reads from, for clocks that disagree.
const importSinceMargin = 10 * time.Minute

// coversTeams reports whether an earlier import, by its stored request, read
// every source team this one asks for. An empty choice means all of them.
func coversTeams(earlier []byte, wanted *[]string) bool {
	var before struct {
		Teams []string `json:"teams"`
	}
	if json.Unmarshal(earlier, &before) != nil {
		return false
	}
	if len(before.Teams) == 0 {
		return true
	}
	if wanted == nil || len(*wanted) == 0 {
		return false
	}
	for _, team := range *wanted {
		if !slices.Contains(before.Teams, team) {
			return false
		}
	}
	return true
}

func slicesContainTeam(teams []dbgen.Team, id uuid.UUID) bool {
	for _, t := range teams {
		if t.ID == id {
			return true
		}
	}
	return false
}

// importOptions is how a request says its import should go: which team
// each source team is sent to, and who each source person is.
func (s *Server) importOptions(ctx context.Context, workspaceID uuid.UUID, req api.ImportRequest) (importer.Options, error) {
	opts := importer.Options{TeamMap: map[string]uuid.UUID{}, UserMap: map[string]*uuid.UUID{}, GitHubLogins: map[string]uuid.UUID{}}
	members, err := s.q.ListWorkspaceMembers(ctx, workspaceID)
	if err != nil {
		return opts, err
	}
	for _, m := range members {
		if m.User.GithubLogin != "" {
			opts.GitHubLogins[strings.ToLower(m.User.GithubLogin)] = m.User.ID
		}
	}
	if req.TeamMap != nil {
		maps.Copy(opts.TeamMap, *req.TeamMap)
	}
	opts.AsProjects = req.AsProjects != nil && *req.AsProjects
	if req.UserMap != nil {
		for externalID, choice := range *req.UserMap {
			if choice == "" {
				opts.UserMap[externalID] = nil
				continue
			}
			id, err := uuid.Parse(choice)
			if err != nil {
				return opts, invalidImport("userMap values are member IDs, or empty for nobody.")
			}
			opts.UserMap[externalID] = &id
		}
	}
	return opts, nil
}

// readChanges makes a source read only what changed since the last import
// of the same origin that ran to the end, if there was one that read all
// this one asks for and the request doesn't ask for everything.
func readChanges(ctx context.Context, q *dbgen.Queries, source importer.Source, workspaceID uuid.UUID, origin string, req api.ImportRequest) error {
	incremental, ok := source.(importer.Incremental)
	if !ok || (req.Full != nil && *req.Full) {
		return nil
	}
	last, err := q.GetLastCompleteImport(ctx, dbgen.GetLastCompleteImportParams{WorkspaceID: workspaceID, Origin: origin})
	if db.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if coversTeams(last.Request, req.Teams) {
		incremental.ReadSince(last.CreatedAt.Add(-importSinceMargin))
	}
	return nil
}

// resumableSource reports whether an import of this kind can be carried on
// with after a restart: one read from a tracker, not from an uploaded file.
func resumableSource(source api.ImportSource) bool {
	return source == api.ImportSourceGithub || source == api.ImportSourceLinear || source == api.ImportSourceJira
}

func (s *Server) StartImport(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	req, ok := decodeImportRequest(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	actor := currentSession(r).User.ID
	source, err := s.importSource(ctx, workspaceId, actor, req)
	if err != nil {
		s.failImport(w, r, err)
		return
	}
	opts, err := s.importOptions(ctx, workspaceId, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// The preview proves the source can be read before anything is promised,
	// and says what the import is called.
	previewCtx, cancel := context.WithTimeout(ctx, importPreviewTimeout)
	preview, err := source.Preview(previewCtx)
	cancel()
	if err != nil {
		s.failImport(w, r, err)
		return
	}

	// What it takes to carry on after a restart, kept only while it runs.
	id := newID()
	var secret []byte
	if s.sealKey != nil && resumableSource(req.Source) {
		whole, err := json.Marshal(req)
		if err == nil {
			secret, err = s.sealKey.Seal(whole, importSecretBound(id))
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}

	var imp dbgen.Import
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		// One at a time per workspace: two imports would trip over each
		// other's teams and numbers.
		if err := q.LockWorkspace(ctx, workspaceId); err != nil {
			return err
		}
		if err := q.FailStaleImports(ctx, workspaceId); err != nil {
			return err
		}
		running, err := q.CountRunningImports(ctx, workspaceId)
		if err != nil {
			return err
		}
		if running > 0 {
			return &apiError{http.StatusConflict, "import_running", "Another import is running in this workspace. Wait for it to finish."}
		}
		imp, err = q.CreateImport(ctx, dbgen.CreateImportParams{
			ID: id, WorkspaceID: workspaceId, Source: string(req.Source), Label: truncate(preview.Label, 200),
			Stage: "Starting", CreatedBy: &actor, Request: rerunnableRequest(req), Origin: preview.Origin, Secret: secret,
		})
		if err != nil {
			return err
		}
		return readChanges(ctx, q, source, workspaceId, preview.Origin, req)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.imports.Add(1)
	go func() {
		defer s.imports.Done()
		s.runImport(imp, source, preview.Origin, actor, opts)
	}()
	writeJSON(w, http.StatusAccepted, importRecord(imp))
}

func importSecretBound(id uuid.UUID) string { return "import:" + id.String() }

// importResumeEvery is how often imports left without a server are looked for.
const importResumeEvery = 30 * time.Second

// ResumeImports carries on with imports whose server went away while they
// ran, until ctx ends. It needs the credentials kept with them, so without
// a SECRETS_KEY there is nothing for it to do.
func (s *Server) ResumeImports(ctx context.Context) {
	if s.sealKey == nil {
		return
	}
	ticker := time.NewTicker(importResumeEvery)
	defer ticker.Stop()
	for {
		if _, err := s.resumeImports(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("resume imports", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// resumeImports claims the imports to carry on with and starts each again,
// returning how many it started. Starting again is safe: an import updates
// what an earlier run made rather than duplicating it.
func (s *Server) resumeImports(ctx context.Context) (int, error) {
	claimed, err := s.q.ClaimImportsToResume(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, imp := range claimed {
		source, actor, opts, err := s.resumedImport(ctx, imp)
		if err != nil {
			message := importMessage(err)
			if message == "" {
				s.log.Error("resume import", "import", imp.ID, "err", err)
				message = "The server restarted and the import could not be carried on with. Run it again: what was already imported is not duplicated."
			}
			if _, err := s.q.FinishImport(ctx, dbgen.FinishImportParams{ID: imp.ID, Status: "failed", Error: message, Counts: imp.Counts, Warnings: imp.Warnings}); err != nil {
				s.log.Error("resume import: record the outcome", "import", imp.ID, "err", err)
			}
			continue
		}
		s.log.Info("resuming import", "import", imp.ID, "source", imp.Source, "attempt", imp.Resumes)
		started++
		s.imports.Add(1)
		go func() {
			defer s.imports.Done()
			s.runImport(imp, source, imp.Origin, actor, opts)
		}()
	}
	return started, nil
}

// resumedImport rebuilds, from what was kept with an import, what it takes
// to run it: its source, who runs it, and how.
func (s *Server) resumedImport(ctx context.Context, imp dbgen.Import) (importer.Source, uuid.UUID, importer.Options, error) {
	var opts importer.Options
	if imp.CreatedBy == nil {
		return nil, uuid.Nil, opts, invalidImport("The server restarted, and whoever started this import is no longer here to carry on as.")
	}
	actor := *imp.CreatedBy
	// Still an admin: the import acts with their say-so.
	role, err := s.q.GetWorkspaceRole(ctx, dbgen.GetWorkspaceRoleParams{WorkspaceID: imp.WorkspaceID, UserID: actor})
	if db.IsNoRows(err) || (err == nil && role != roleAdmin) {
		return nil, actor, opts, invalidImport("The server restarted, and whoever started this import is no longer an admin here.")
	}
	if err != nil {
		return nil, actor, opts, err
	}
	whole, err := s.sealKey.Open(imp.Secret, importSecretBound(imp.ID))
	if err != nil {
		return nil, actor, opts, fmt.Errorf("open the import's request: %w", err)
	}
	var req api.ImportRequest
	if err := json.Unmarshal(whole, &req); err != nil {
		return nil, actor, opts, err
	}
	source, err := s.importSource(ctx, imp.WorkspaceID, actor, req)
	if err != nil {
		return nil, actor, opts, err
	}
	if opts, err = s.importOptions(ctx, imp.WorkspaceID, req); err != nil {
		return nil, actor, opts, err
	}
	return source, actor, opts, readChanges(ctx, s.q, source, imp.WorkspaceID, imp.Origin, req)
}

// runImport carries an import through, on its own goroutine: it outlives
// the request that started it, and ends when the server shuts down.
func (s *Server) runImport(imp dbgen.Import, source importer.Source, origin string, actor uuid.UUID, opts importer.Options) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.closing:
			cancel()
		case <-ctx.Done():
		}
	}()

	var (
		mu     sync.Mutex
		stage  = "Starting"
		loader *importer.Loader
	)
	// The row is refreshed on a timer rather than on every step, so a source
	// that is slow (or waiting out a rate limit) still shows signs of life.
	report := func() {
		mu.Lock()
		current, running := stage, loader
		mu.Unlock()
		counts := map[string]int{}
		if running != nil {
			counts = running.Progress()
		}
		encoded, _ := json.Marshal(counts)
		if err := s.q.UpdateImportProgress(ctx, dbgen.UpdateImportProgressParams{ID: imp.ID, Stage: current, Counts: encoded}); err != nil && ctx.Err() == nil {
			s.log.Warn("import: record progress", "err", err)
		}
	}
	ticking := make(chan struct{})
	go func() {
		ticker := time.NewTicker(importProgressEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticking:
				return
			case <-ticker.C:
				report()
			}
		}
	}()

	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panic: %v", p)
			}
		}()
		made, err := importer.NewLoader(ctx, s.pool, imp.WorkspaceID, imp.ID, actor, origin, opts)
		if err != nil {
			return err
		}
		// Attachments come across too, read with the source's credentials.
		made.CopyFiles(s.blobs, source)
		mu.Lock()
		loader = made
		mu.Unlock()
		err = importer.Read(ctx, source,
			func(b importer.Bundle) error { return made.Load(ctx, b) },
			func(now string) {
				mu.Lock()
				stage = now
				mu.Unlock()
			},
		)
		// Files are copied behind the reading; they may still be going.
		if counts := made.Progress(); err == nil && counts["files"] > 0 {
			mu.Lock()
			stage = "Copying files"
			mu.Unlock()
		}
		if filesErr := made.WaitFiles(); err == nil {
			err = filesErr
		}
		// What did arrive is tied together and shown to everyone, even if
		// the source gave out part way: running the import again picks up
		// from there.
		mu.Lock()
		stage = "Finishing"
		mu.Unlock()
		// Not the import's own context: a shutdown must not skip this.
		finishCtx, cancelFinish := context.WithTimeout(context.Background(), time.Minute)
		defer cancelFinish()
		if finishErr := made.Finish(finishCtx); err == nil {
			err = finishErr
		}
		return err
	}()
	close(ticking)

	// A server shutting down hands the import on rather than failing it, if
	// what it takes to carry on was kept.
	select {
	case <-s.closing:
		if err != nil && len(imp.Secret) > 0 {
			releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancelRelease()
			if err := s.q.ReleaseImport(releaseCtx, imp.ID); err != nil {
				s.log.Error("import: hand on", "import", imp.ID, "err", err)
			}
			return
		}
	default:
	}

	status, message := "done", ""
	if err != nil {
		status = "failed"
		if message = importMessage(err); message == "" {
			s.log.Error("import failed", "import", imp.ID, "source", imp.Source, "err", err)
			message = "The import stopped: " + truncate(err.Error(), 500) + " What was already imported is kept; running it again continues from there."
		}
	}
	counts, warnings := map[string]int{}, []string{}
	if loader != nil {
		counts, warnings = loader.Counts, loader.Warnings
		if warnings == nil {
			warnings = []string{}
		}
	}
	encoded, _ := json.Marshal(counts)
	finishCtx, cancelFinish := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFinish()
	if _, err := s.q.FinishImport(finishCtx, dbgen.FinishImportParams{ID: imp.ID, Status: status, Error: message, Counts: encoded, Warnings: warnings}); err != nil {
		s.log.Error("import: record the outcome", "import", imp.ID, "err", err)
	}
}

func (s *Server) ListImports(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	ctx := r.Context()
	if err := s.q.FailStaleImports(ctx, workspaceId); err != nil {
		s.internalError(w, r, err)
		return
	}
	rows, err := s.q.ListImports(ctx, workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.Import, 0, len(rows))
	for _, row := range rows {
		out = append(out, importRecord(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// requireImport loads an import in a workspace the caller administers.
func (s *Server) requireImport(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Import, bool) {
	imp, err := s.q.GetImport(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return imp, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return imp, false
	}
	if !s.requireWorkspaceAdmin(w, r, imp.WorkspaceID) {
		return imp, false
	}
	return imp, true
}

func (s *Server) GetImport(w http.ResponseWriter, r *http.Request, importId uuid.UUID) {
	imp, ok := s.requireImport(w, r, importId)
	if !ok {
		return
	}
	ctx := r.Context()
	if imp.Status == "running" {
		// It may have died with its server since it was last heard from.
		if err := s.q.FailStaleImports(ctx, imp.WorkspaceID); err != nil {
			s.internalError(w, r, err)
			return
		}
		var err error
		if imp, err = s.q.GetImport(ctx, importId); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, importRecord(imp))
}

func (s *Server) UndoImport(w http.ResponseWriter, r *http.Request, importId uuid.UUID) {
	imp, ok := s.requireImport(w, r, importId)
	if !ok {
		return
	}
	switch imp.Status {
	case "running":
		writeError(w, http.StatusConflict, "import_running", "The import is still running. Undo it once it has finished.")
		return
	case "undone":
		writeJSON(w, http.StatusOK, importRecord(imp))
		return
	}
	undone, err := importer.Undo(r.Context(), s.pool, s.blobs, imp)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, importRecord(undone))
}
