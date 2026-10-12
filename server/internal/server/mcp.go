package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/config"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/mcp"
)

// The assistant endpoint: /api/mcp speaks the Model Context Protocol, so an
// assistant such as Claude can read and change a workspace through a handful
// of tools. It is reached with an API key, acts as the key's owner in the
// key's workspace, and can do nothing that person couldn't do in the app:
// every change goes through the same handlers the web app calls.

const mcpInstructions = `Cruuze is this team's issue tracker. An issue is named by its team key and number, like ENG-12.

Call get_context first: it gives the team keys, each team's statuses, the people, labels and projects, which are the words the other tools take. Tools accept names (a status name, a person's name or email or "me", a label, a project) rather than IDs, and say what the valid choices are when a name doesn't match.

list_issues filters by fields; search_issues finds words in titles, descriptions and comments. Read an issue with get_issue before changing it. Descriptions and comments are Markdown.`

// mcpHandler serves the assistant endpoint.
func (s *Server) mcpHandler() http.Handler {
	server := &mcp.Server{
		Name:         "cruuze",
		Title:        "Cruuze",
		Version:      config.Version,
		Instructions: mcpInstructions,
		Tools:        s.mcpTools(),
		OnError: func(r *http.Request, tool string, err error) {
			s.log.Error("mcp tool failed", "tool", tool, "err", err)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A browser session spans workspaces; a key names the one to work in.
		if currentSession(r).keyWorkspace == nil {
			writeError(w, http.StatusForbidden, "api_key_required", "Connect with an API key (Settings → API): Authorization: Bearer cruuze_…")
			return
		}
		server.ServeHTTP(w, r)
	})
}

// --- Calling the API's own handlers --------------------------------------------

// memResponse collects what a handler writes.
type memResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (m *memResponse) Header() http.Header { return m.header }
func (m *memResponse) WriteHeader(status int) {
	if m.status == 0 {
		m.status = status
	}
}
func (m *memResponse) Write(b []byte) (int, error) {
	m.WriteHeader(http.StatusOK)
	return m.body.Write(b)
}

// callAPI runs one of the API's handlers as the caller of r, with body as
// the request body, and decodes its answer into out. This is how tools
// change things: permissions, validation, sync and notifications are the
// handler's, exactly as for the web app. A refusal becomes a tool error
// carrying the handler's own message.
func (s *Server) callAPI(r *http.Request, body, out any, handler func(http.ResponseWriter, *http.Request)) error {
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return err
		}
	}
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(&payload)
	res := &memResponse{header: http.Header{}}
	handler(res, req)
	if res.status >= 500 {
		return fmt.Errorf("handler answered %d: %s", res.status, res.body.String())
	}
	if res.status >= 400 {
		var apiErr api.Error
		if json.Unmarshal(res.body.Bytes(), &apiErr) != nil || apiErr.Message == "" {
			apiErr.Message = http.StatusText(res.status)
		}
		return mcp.Fail(apiErr.Message)
	}
	if out == nil || res.body.Len() == 0 {
		return nil
	}
	return json.Unmarshal(res.body.Bytes(), out)
}

// --- The workspace as the caller sees it ------------------------------------------

// mcpWorld is a workspace's vocabulary (teams, statuses, people, labels,
// projects) limited to what the caller may see. Tools resolve the names an
// assistant gives against it and name things in their answers from it.
type mcpWorld struct {
	ws       dbgen.Workspace
	me       dbgen.User
	role     string
	teams    []dbgen.Team
	teamIDs  []uuid.UUID
	states   []dbgen.WorkflowState
	members  []dbgen.ListWorkspaceMembersRow
	labels   []dbgen.Label
	projects []dbgen.Project
	appURL   string
}

func (s *Server) mcpWorld(ctx context.Context, r *http.Request) (*mcpWorld, error) {
	session := currentSession(r)
	w := &mcpWorld{me: session.User, appURL: s.cfg.AppURL}
	workspaceID := *session.keyWorkspace
	var err error
	if w.ws, err = s.q.GetWorkspace(ctx, workspaceID); err != nil {
		return nil, err
	}
	if w.role, err = s.q.GetWorkspaceRole(ctx, dbgen.GetWorkspaceRoleParams{WorkspaceID: workspaceID, UserID: w.me.ID}); err != nil {
		return nil, err
	}
	teams, err := s.q.ListTeamsForUser(ctx, dbgen.ListTeamsForUserParams{WorkspaceID: workspaceID, UserID: w.me.ID, MembersOnly: w.role == roleGuest})
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		w.teams = append(w.teams, t.Team)
		w.teamIDs = append(w.teamIDs, t.Team.ID)
	}
	if w.states, err = s.q.ListWorkflowStatesForTeams(ctx, w.teamIDs); err != nil {
		return nil, err
	}
	if w.members, err = s.q.ListWorkspaceMembers(ctx, workspaceID); err != nil {
		return nil, err
	}
	if w.labels, err = s.q.ListLabels(ctx, workspaceID); err != nil {
		return nil, err
	}
	if w.projects, err = s.q.ListProjects(ctx, workspaceID); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *mcpWorld) team(id uuid.UUID) (dbgen.Team, bool) {
	i := slices.IndexFunc(w.teams, func(t dbgen.Team) bool { return t.ID == id })
	if i < 0 {
		return dbgen.Team{}, false
	}
	return w.teams[i], true
}

func (w *mcpWorld) state(id uuid.UUID) dbgen.WorkflowState {
	i := slices.IndexFunc(w.states, func(s dbgen.WorkflowState) bool { return s.ID == id })
	if i < 0 {
		return dbgen.WorkflowState{}
	}
	return w.states[i]
}

func (w *mcpWorld) teamStates(teamID uuid.UUID) []dbgen.WorkflowState {
	var out []dbgen.WorkflowState
	for _, s := range w.states {
		if s.TeamID == teamID {
			out = append(out, s)
		}
	}
	return out
}

func (w *mcpWorld) userName(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	for _, m := range w.members {
		if m.User.ID == *id {
			return displayName(m.User)
		}
	}
	return ""
}

func (w *mcpWorld) labelNames(ids []uuid.UUID) []string {
	var out []string
	for _, l := range w.labels {
		if slices.Contains(ids, l.ID) {
			out = append(out, l.Name)
		}
	}
	return out
}

func (w *mcpWorld) projectName(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	for _, p := range w.projects {
		if p.ID == *id {
			return p.Name
		}
	}
	return ""
}

// --- Resolving the names an assistant gives -----------------------------------------

// listed joins names for an error message, so the assistant can pick one.
func listed(names []string) string {
	const most = 30
	if len(names) > most {
		return strings.Join(names[:most], ", ") + fmt.Sprintf(" and %d more", len(names)-most)
	}
	if len(names) == 0 {
		return "(there are none)"
	}
	return strings.Join(names, ", ")
}

// findTeam takes a team key or name. With none given, the only team is meant.
func (w *mcpWorld) findTeam(arg string) (dbgen.Team, error) {
	arg = strings.TrimSpace(arg)
	names := make([]string, 0, len(w.teams))
	for _, t := range w.teams {
		if strings.EqualFold(t.Key, arg) || strings.EqualFold(t.Name, arg) {
			return t, nil
		}
		names = append(names, fmt.Sprintf("%s (%s)", t.Key, t.Name))
	}
	if arg == "" && len(w.teams) == 1 {
		return w.teams[0], nil
	}
	if arg == "" {
		return dbgen.Team{}, mcp.Fail("Say which team. Teams: " + listed(names))
	}
	return dbgen.Team{}, mcp.Fail(fmt.Sprintf("No team %q. Teams: %s", arg, listed(names)))
}

// findState takes one of a team's status names, or a kind of status
// ("started"), which means the first status of that kind.
func (w *mcpWorld) findState(team dbgen.Team, arg string) (dbgen.WorkflowState, error) {
	arg = strings.TrimSpace(arg)
	states := w.teamStates(team.ID)
	names := make([]string, 0, len(states))
	for _, s := range states {
		if strings.EqualFold(s.Name, arg) {
			return s, nil
		}
		names = append(names, s.Name)
	}
	for _, s := range states {
		if strings.EqualFold(s.Type, arg) {
			return s, nil
		}
	}
	return dbgen.WorkflowState{}, mcp.Fail(fmt.Sprintf("Team %s has no status %q. Its statuses: %s", team.Key, arg, listed(names)))
}

// findUser takes "me", an email, a name, or an unambiguous part of either.
func (w *mcpWorld) findUser(arg string) (dbgen.User, error) {
	arg = strings.TrimSpace(arg)
	if strings.EqualFold(arg, "me") {
		return w.me, nil
	}
	var partial []dbgen.User
	names := make([]string, 0, len(w.members))
	for _, m := range w.members {
		if strings.EqualFold(m.User.Email, arg) || strings.EqualFold(m.User.Name, arg) || m.User.ID.String() == arg {
			return m.User, nil
		}
		lower := strings.ToLower(arg)
		if arg != "" && (strings.Contains(strings.ToLower(m.User.Name), lower) || strings.Contains(strings.ToLower(m.User.Email), lower)) {
			partial = append(partial, m.User)
		}
		names = append(names, fmt.Sprintf("%s <%s>", displayName(m.User), m.User.Email))
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	if len(partial) > 1 {
		matches := make([]string, 0, len(partial))
		for _, u := range partial {
			matches = append(matches, fmt.Sprintf("%s <%s>", displayName(u), u.Email))
		}
		return dbgen.User{}, mcp.Fail(fmt.Sprintf("%q matches several people: %s. Use the email.", arg, listed(matches)))
	}
	return dbgen.User{}, mcp.Fail(fmt.Sprintf("Nobody called %q here. People: %s", arg, listed(names)))
}

func (w *mcpWorld) findLabels(args []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(args))
	for _, arg := range args {
		i := slices.IndexFunc(w.labels, func(l dbgen.Label) bool { return strings.EqualFold(l.Name, strings.TrimSpace(arg)) })
		if i < 0 {
			names := make([]string, 0, len(w.labels))
			for _, l := range w.labels {
				names = append(names, l.Name)
			}
			return nil, mcp.Fail(fmt.Sprintf("No label %q. Labels: %s", arg, listed(names)))
		}
		ids = append(ids, w.labels[i].ID)
	}
	return ids, nil
}

// findProject takes a project's name, an unambiguous part of it, or its ID.
func (w *mcpWorld) findProject(arg string) (dbgen.Project, error) {
	arg = strings.TrimSpace(arg)
	var partial []dbgen.Project
	names := make([]string, 0, len(w.projects))
	for _, p := range w.projects {
		if strings.EqualFold(p.Name, arg) || p.ID.String() == arg {
			return p, nil
		}
		if arg != "" && strings.Contains(strings.ToLower(p.Name), strings.ToLower(arg)) {
			partial = append(partial, p)
		}
		names = append(names, p.Name)
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	return dbgen.Project{}, mcp.Fail(fmt.Sprintf("No project %q. Projects: %s", arg, listed(names)))
}

var priorityNames = []string{"none", "urgent", "high", "medium", "low"}

func parsePriority(arg string) (int32, error) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if i := slices.Index(priorityNames, arg); i >= 0 {
		return int32(i), nil
	}
	if n, err := strconv.Atoi(arg); err == nil && n >= 0 && n <= 4 {
		return int32(n), nil
	}
	return 0, mcp.Fail(fmt.Sprintf("No priority %q. Priorities: urgent, high, medium, low, none", arg))
}

func parseDay(name, arg string) (time.Time, error) {
	day, err := time.Parse(time.DateOnly, strings.TrimSpace(arg))
	if err != nil {
		return day, mcp.Fail(fmt.Sprintf("%s must be a date like 2026-10-31, not %q", name, arg))
	}
	return day, nil
}

// findIssue takes KEY-number. An issue in a team the caller can't see is
// reported exactly like one that doesn't exist.
func (s *Server) findIssue(ctx context.Context, w *mcpWorld, ref string) (dbgen.Issue, dbgen.Team, error) {
	missing := mcp.Fail(fmt.Sprintf("No issue %q. Issues are named like ENG-12: a team key and a number.", ref))
	key, number, ok := parseIssueRef(ref)
	if !ok {
		return dbgen.Issue{}, dbgen.Team{}, missing
	}
	issue, err := s.q.GetIssueByTeamKeyAndNumber(ctx, dbgen.GetIssueByTeamKeyAndNumberParams{WorkspaceID: w.ws.ID, Key: key, Number: number})
	if db.IsNoRows(err) {
		return issue, dbgen.Team{}, missing
	}
	if err != nil {
		return issue, dbgen.Team{}, err
	}
	team, visible := w.team(issue.TeamID)
	if !visible {
		return issue, team, missing
	}
	return issue, team, nil
}

// --- What tools answer with -----------------------------------------------------------

// mcpIssue is an issue in the words of the workspace rather than its IDs.
type mcpIssue struct {
	Ref        string   `json:"ref"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	StatusKind string   `json:"statusKind"`
	Priority   string   `json:"priority,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Project    string   `json:"project,omitempty"`
	Due        string   `json:"due,omitempty"`
	Created    string   `json:"created"`
	Updated    string   `json:"updated"`
	Archived   bool     `json:"archived,omitempty"`
	URL        string   `json:"url"`
}

func (w *mcpWorld) ref(issue dbgen.Issue) string {
	team, _ := w.team(issue.TeamID)
	return fmt.Sprintf("%s-%d", team.Key, issue.Number)
}

func (w *mcpWorld) issue(i dbgen.Issue) mcpIssue {
	state := w.state(i.StateID)
	ref := w.ref(i)
	out := mcpIssue{
		Ref:        ref,
		Title:      i.Title,
		Status:     state.Name,
		StatusKind: state.Type,
		Assignee:   w.userName(i.AssigneeID),
		Labels:     w.labelNames(i.LabelIds),
		Project:    w.projectName(i.ProjectID),
		Created:    i.CreatedAt.UTC().Format(time.DateOnly),
		Updated:    i.UpdatedAt.UTC().Format(time.DateOnly),
		Archived:   i.ArchivedAt != nil,
		URL:        fmt.Sprintf("%s/%s/issue/%s", w.appURL, w.ws.UrlKey, ref),
	}
	if i.Priority > 0 && int(i.Priority) < len(priorityNames) {
		out.Priority = priorityNames[i.Priority]
	}
	if i.DueDate != nil {
		out.Due = i.DueDate.Format(time.DateOnly)
	}
	return out
}

// --- Describing tools --------------------------------------------------------------------

type schemaProps map[string]any

func object(props schemaProps, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func text(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func choice(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func number(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func flag(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func texts(description string) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": map[string]any{"type": "string"}}
}
