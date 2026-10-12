package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/mcp"
)

const (
	mcpDefaultIssues = 30
	mcpMaxIssues     = 100
	mcpMaxComments   = 30
)

// mcpTools are what an assistant can do. Reads ask the database directly
// (limited to the teams the caller can see); every change goes through the
// API's own handler for it.
func (s *Server) mcpTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "get_context", Title: "Workspace overview", ReadOnly: true,
			Description: "The workspace's vocabulary: who you are acting as, the teams and each team's statuses, the people, labels and projects. Call this first; the other tools take these names.",
			Call:        s.toolContext,
		},
		{
			Name: "list_issues", Title: "List issues", ReadOnly: true,
			Description: "Issues that match every filter given, newest change first unless sorted otherwise. With no filters: open issues in all teams. For words in the text, use search_issues instead.",
			Input: object(schemaProps{
				"team":            text("Team key or name. Omit for all teams."),
				"assignee":        text(`A person's name or email, "me", or "none" for unassigned.`),
				"status":          text(`"open" (the default: not completed or canceled), "closed", "any", a kind (triage, backlog, unstarted, started, completed, canceled), or a status name such as "In Review".`),
				"project":         text("Project name."),
				"label":           text("Label name."),
				"cycle":           text(`"current" for the running cycle of the team (or of every team).`),
				"parent":          text("Only sub-issues of this issue, e.g. ENG-12."),
				"dueBy":           text("Only issues due on or before this date (YYYY-MM-DD)."),
				"untouchedDays":   number("Only issues nobody has changed for at least this many days."),
				"sort":            choice("Order of the results.", "updated", "priority", "due", "created"),
				"limit":           number(fmt.Sprintf("How many to return: default %d, at most %d.", mcpDefaultIssues, mcpMaxIssues)),
				"includeArchived": flag("Also include archived issues."),
			}),
			Call: s.toolListIssues,
		},
		{
			Name: "search_issues", Title: "Search issues", ReadOnly: true,
			Description: "Full-text search over issue titles, descriptions and comments, archived issues included. All the words must appear in the same issue or the same comment.",
			Input:       object(schemaProps{"query": text("Words to look for.")}, "query"),
			Call:        s.toolSearchIssues,
		},
		{
			Name: "get_issue", Title: "Read an issue", ReadOnly: true,
			Description: "Everything about one issue: its description, properties, sub-issues, what it blocks or waits on, pull requests, comments, time logged and the customer requests waiting on it.",
			Input:       object(schemaProps{"ref": text("The issue, e.g. ENG-12.")}, "ref"),
			Call:        s.toolGetIssue,
		},
		{
			Name: "create_issue", Title: "Create an issue",
			Description: "Creates an issue. It goes to the team's backlog unless a status is given. Check for an existing issue first (search_issues) rather than filing a duplicate.",
			Input: object(schemaProps{
				"team":        text("Team key or name. May be omitted when there is only one team."),
				"title":       text("A short title."),
				"description": text("Markdown."),
				"status":      text("One of the team's status names."),
				"priority":    choice("How pressing it is.", "urgent", "high", "medium", "low", "none"),
				"assignee":    text(`A person's name or email, or "me".`),
				"labels":      texts("Label names."),
				"project":     text("Project name."),
				"parent":      text("Make it a sub-issue of this issue, e.g. ENG-12."),
				"dueDate":     text("YYYY-MM-DD."),
			}, "title"),
			Call: s.toolCreateIssue,
		},
		{
			Name: "update_issue", Title: "Change an issue",
			Description: `Changes the given fields of an issue and leaves the rest alone. To close an issue, set its status to one of the team's completed or canceled statuses. "none" clears assignee, project, parent or dueDate.`,
			Input: object(schemaProps{
				"ref":          text("The issue, e.g. ENG-12."),
				"title":        text("New title."),
				"description":  text("New description, replacing the old one. Markdown."),
				"status":       text("One of the team's status names."),
				"priority":     choice("How pressing it is.", "urgent", "high", "medium", "low", "none"),
				"assignee":     text(`A person's name or email, "me", or "none".`),
				"addLabels":    texts("Label names to add."),
				"removeLabels": texts("Label names to remove."),
				"project":      text(`Project name, or "none".`),
				"parent":       text(`The issue this is a sub-issue of, or "none".`),
				"dueDate":      text(`YYYY-MM-DD, or "none".`),
			}, "ref"),
			Call: s.toolUpdateIssue,
		},
		{
			Name: "add_comment", Title: "Comment on an issue",
			Description: "Adds a comment to an issue as you. The people following the issue are notified.",
			Input:       object(schemaProps{"ref": text("The issue, e.g. ENG-12."), "body": text("Markdown.")}, "ref", "body"),
			Call:        s.toolAddComment,
		},
		{
			Name: "log_time", Title: "Log time on an issue",
			Description: "Records time you spent on an issue, ending now.",
			Input: object(schemaProps{
				"ref":     text("The issue, e.g. ENG-12."),
				"minutes": number("How long, in minutes."),
				"note":    text("What it was spent on."),
			}, "ref", "minutes"),
			Call: s.toolLogTime,
		},
		{
			Name: "get_project", Title: "Read a project", ReadOnly: true,
			Description: "A project's status, dates, progress, latest health updates, what it waits on, and a forecast of when it will be done (dates it is 50%, 85% and 95% likely to finish by, from the team's real pace).",
			Input:       object(schemaProps{"project": text("Project name.")}, "project"),
			Call:        s.toolGetProject,
		},
		{
			Name: "post_project_update", Title: "Post a project update",
			Description: "Posts a dated note on how a project is going. The newest update is the project's health.",
			Input: object(schemaProps{
				"project": text("Project name."),
				"health":  choice("How it is going.", "onTrack", "atRisk", "offTrack"),
				"body":    text("What changed since the last update. Markdown."),
			}, "project", "health"),
			Call: s.toolPostProjectUpdate,
		},
		{
			Name: "team_insights", Title: "Team flow numbers", ReadOnly: true,
			Description: "How a team's work has been flowing over the last twelve weeks: cycle time, issues completed and created per week, work in progress and how long it has been open. Team-level only; there are no per-person numbers.",
			Input:       object(schemaProps{"team": text("Team key or name. May be omitted when there is only one team.")}),
			Call:        s.toolTeamInsights,
		},
		{
			Name: "list_customer_requests", Title: "List customer requests", ReadOnly: true,
			Description: "What customers have asked for through the portal, most voted first, with the issue each is tied to.",
			Input:       object(schemaProps{"status": choice(`"open" (the default) leaves out what was shipped or declined.`, "open", "all")}),
			Call:        s.toolCustomerRequests,
		},
	}
}

// --- get_context ---------------------------------------------------------------

func (s *Server) toolContext(ctx context.Context, r *http.Request, _ json.RawMessage) (any, error) {
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	type status struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	type team struct {
		Key          string   `json:"key"`
		Name         string   `json:"name"`
		Kind         string   `json:"kind"`
		Statuses     []status `json:"statuses"`
		CurrentCycle string   `json:"currentCycle,omitempty"`
	}
	type person struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	type project struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Target string `json:"target,omitempty"`
		Lead   string `json:"lead,omitempty"`
	}
	out := struct {
		Workspace  string    `json:"workspace"`
		URL        string    `json:"url"`
		Me         person    `json:"me"`
		Today      string    `json:"today"`
		Teams      []team    `json:"teams"`
		People     []person  `json:"people"`
		Labels     []string  `json:"labels"`
		Projects   []project `json:"projects"`
		Priorities []string  `json:"priorities"`
	}{
		Workspace:  w.ws.Name,
		URL:        w.appURL + "/" + w.ws.UrlKey,
		Me:         person{Name: displayName(w.me), Email: w.me.Email, Role: w.role},
		Today:      time.Now().UTC().Format(time.DateOnly),
		Teams:      []team{},
		People:     []person{},
		Labels:     []string{},
		Projects:   []project{},
		Priorities: []string{"urgent", "high", "medium", "low", "none"},
	}
	cycles, err := s.q.ListCurrentCycles(ctx, dbgen.ListCurrentCyclesParams{TeamIds: w.teamIDs, Day: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	for _, t := range w.teams {
		entry := team{Key: t.Key, Name: t.Name, Kind: t.Kind, Statuses: []status{}}
		for _, st := range w.teamStates(t.ID) {
			entry.Statuses = append(entry.Statuses, status{Name: st.Name, Kind: st.Type})
		}
		for _, c := range cycles {
			if c.TeamID == t.ID {
				entry.CurrentCycle = fmt.Sprintf("Cycle %d, %s to %s", c.Number, c.StartsAt.Format(time.DateOnly), c.EndsAt.Format(time.DateOnly))
			}
		}
		out.Teams = append(out.Teams, entry)
	}
	for _, m := range w.members {
		out.People = append(out.People, person{Name: displayName(m.User), Email: m.User.Email, Role: m.Role})
	}
	for _, l := range w.labels {
		out.Labels = append(out.Labels, l.Name)
	}
	for _, p := range w.projects {
		entry := project{Name: p.Name, Status: p.Status, Lead: w.userName(p.LeadID)}
		if p.TargetDate != nil {
			entry.Target = p.TargetDate.Format(time.DateOnly)
		}
		out.Projects = append(out.Projects, entry)
	}
	return out, nil
}

// --- list_issues -----------------------------------------------------------------

var (
	openStateTypes   = []string{"triage", "backlog", "unstarted", "started"}
	closedStateTypes = []string{"completed", "canceled"}
	allStateTypes    = append(slices.Clone(openStateTypes), closedStateTypes...)
)

func (s *Server) toolListIssues(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Team            string `json:"team"`
		Assignee        string `json:"assignee"`
		Status          string `json:"status"`
		Project         string `json:"project"`
		Label           string `json:"label"`
		Cycle           string `json:"cycle"`
		Parent          string `json:"parent"`
		DueBy           string `json:"dueBy"`
		UntouchedDays   int    `json:"untouchedDays"`
		Sort            string `json:"sort"`
		Limit           int    `json:"limit"`
		IncludeArchived bool   `json:"includeArchived"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	teams := w.teams
	if in.Team != "" {
		team, err := w.findTeam(in.Team)
		if err != nil {
			return nil, err
		}
		teams = []dbgen.Team{team}
	}
	params := dbgen.ListIssuesFilteredParams{
		IncludeArchived: in.IncludeArchived,
		StateTypes:      []string{},
		StateIds:        []uuid.UUID{},
		CycleIds:        []uuid.UUID{},
		Sort:            in.Sort,
	}
	for _, t := range teams {
		params.TeamIds = append(params.TeamIds, t.ID)
	}

	switch status := strings.ToLower(strings.TrimSpace(in.Status)); {
	case status == "" || status == "open":
		params.StateTypes = openStateTypes
	case status == "closed":
		params.StateTypes = closedStateTypes
	case status == "any" || status == "all":
	case slices.Contains(allStateTypes, status):
		params.StateTypes = []string{status}
	default:
		// A status name: it may exist in several teams.
		var names []string
		for _, st := range w.states {
			if !slices.Contains(params.TeamIds, st.TeamID) {
				continue
			}
			if strings.EqualFold(st.Name, status) {
				params.StateIds = append(params.StateIds, st.ID)
			}
			if !slices.Contains(names, st.Name) {
				names = append(names, st.Name)
			}
		}
		if len(params.StateIds) == 0 {
			return nil, mcp.Fail(fmt.Sprintf("No status %q. Use open, closed, any, a kind (%s) or a status name: %s", in.Status, strings.Join(allStateTypes, ", "), listed(names)))
		}
	}

	switch assignee := strings.TrimSpace(in.Assignee); {
	case assignee == "":
	case strings.EqualFold(assignee, "none") || strings.EqualFold(assignee, "unassigned"):
		params.Unassigned = true
	default:
		user, err := w.findUser(assignee)
		if err != nil {
			return nil, err
		}
		params.AssigneeID = &user.ID
	}
	if in.Project != "" {
		project, err := w.findProject(in.Project)
		if err != nil {
			return nil, err
		}
		params.ProjectID = &project.ID
	}
	if in.Label != "" {
		ids, err := w.findLabels([]string{in.Label})
		if err != nil {
			return nil, err
		}
		params.LabelID = &ids[0]
	}
	if in.Parent != "" {
		parent, _, err := s.findIssue(ctx, w, in.Parent)
		if err != nil {
			return nil, err
		}
		params.ParentID = &parent.ID
	}
	if in.Cycle != "" {
		if !strings.EqualFold(strings.TrimSpace(in.Cycle), "current") {
			return nil, mcp.Fail(`cycle takes "current".`)
		}
		cycles, err := s.q.ListCurrentCycles(ctx, dbgen.ListCurrentCyclesParams{TeamIds: params.TeamIds, Day: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		if len(cycles) == 0 {
			return nil, mcp.Fail("No cycle is running: cycles are off for these teams.")
		}
		for _, c := range cycles {
			params.CycleIds = append(params.CycleIds, c.ID)
		}
	}
	if in.DueBy != "" {
		day, err := parseDay("dueBy", in.DueBy)
		if err != nil {
			return nil, err
		}
		params.DueBy = &day
	}
	if in.UntouchedDays > 0 {
		since := time.Now().AddDate(0, 0, -in.UntouchedDays)
		params.UntouchedSince = &since
	}
	if in.Sort != "" && !slices.Contains([]string{"updated", "priority", "due", "created"}, in.Sort) {
		return nil, mcp.Fail("sort is one of: updated, priority, due, created")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = mcpDefaultIssues
	}
	limit = min(limit, mcpMaxIssues)
	// One more than asked for, to know whether there are more.
	params.MaxRows = int32(limit + 1)

	rows, err := s.q.ListIssuesFiltered(ctx, params)
	if err != nil {
		return nil, err
	}
	out := struct {
		Issues []mcpIssue `json:"issues"`
		// More says the list was cut off at the limit.
		More bool `json:"more"`
	}{Issues: []mcpIssue{}, More: len(rows) > limit}
	for _, row := range rows[:min(len(rows), limit)] {
		out.Issues = append(out.Issues, w.issue(row))
	}
	return out, nil
}

// --- search_issues ---------------------------------------------------------------

func (s *Server) toolSearchIssues(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, mcp.Fail("query is required")
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	var found api.SearchResults
	err = s.callAPI(r, nil, &found, func(rw http.ResponseWriter, req *http.Request) {
		s.SearchIssues(rw, req, w.ws.ID, api.SearchIssuesParams{Q: in.Query})
	})
	if err != nil {
		return nil, err
	}
	type result struct {
		Ref    string `json:"ref"`
		Title  string `json:"title"`
		Status string `json:"status"`
		// Excerpt is the matching text; In says where it was found.
		Excerpt  string `json:"excerpt,omitempty"`
		In       string `json:"in"`
		Archived bool   `json:"archived,omitempty"`
	}
	out := struct {
		Results []result `json:"results"`
	}{Results: []result{}}
	marks := strings.NewReplacer("<mark>", "", "</mark>", "")
	for _, hit := range found.Results {
		entry := result{Ref: hit.Identifier, Title: hit.Title, Status: w.state(hit.StateId).Name, Excerpt: marks.Replace(hit.Snippet), In: "issue", Archived: hit.Archived}
		if hit.InComment {
			entry.In = "comment"
		}
		out.Results = append(out.Results, entry)
	}
	return out, nil
}

// --- get_issue -------------------------------------------------------------------

func (s *Server) toolGetIssue(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Ref string `json:"ref"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	issue, _, err := s.findIssue(ctx, w, in.Ref)
	if err != nil {
		return nil, err
	}

	type brief struct {
		Ref    string `json:"ref"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	type relation struct {
		// How this issue relates to the other: "blocks", "blocked by", ...
		Relation string `json:"relation"`
		brief
	}
	type pullRequest struct {
		Title  string `json:"title"`
		URL    string `json:"url"`
		Status string `json:"status"`
		Branch string `json:"branch"`
	}
	type comment struct {
		Author string `json:"author"`
		At     string `json:"at"`
		Body   string `json:"body"`
	}
	type request struct {
		Title     string `json:"title"`
		Requester string `json:"requester"`
		Status    string `json:"status"`
		Votes     int32  `json:"votes"`
	}
	out := struct {
		mcpIssue
		Description      string        `json:"description"`
		Creator          string        `json:"creator,omitempty"`
		Parent           *brief        `json:"parent,omitempty"`
		SubIssues        []brief       `json:"subIssues,omitempty"`
		Relations        []relation    `json:"relations,omitempty"`
		PullRequests     []pullRequest `json:"pullRequests,omitempty"`
		CustomerRequests []request     `json:"customerRequests,omitempty"`
		// Minutes in started statuses, from the status history, and minutes people logged.
		MinutesInProgress int64     `json:"minutesInProgress"`
		MinutesLogged     int32     `json:"minutesLogged"`
		Comments          []comment `json:"comments"`
		// EarlierComments is how many older comments were left out.
		EarlierComments int `json:"earlierComments,omitempty"`
	}{mcpIssue: w.issue(issue), Description: issue.Description, Creator: w.userName(issue.CreatorID), MinutesLogged: issue.LoggedSeconds / 60, Comments: []comment{}}

	// Issues in teams the caller can't see are left out of every list below.
	briefOf := func(other dbgen.Issue) (brief, bool) {
		if _, visible := w.team(other.TeamID); !visible {
			return brief{}, false
		}
		return brief{Ref: w.ref(other), Title: other.Title, Status: w.state(other.StateID).Name}, true
	}
	if issue.ParentID != nil {
		if parent, err := s.q.GetIssue(ctx, *issue.ParentID); err == nil {
			if b, ok := briefOf(parent); ok {
				out.Parent = &b
			}
		} else if !db.IsNoRows(err) {
			return nil, err
		}
	}
	subs, err := s.q.ListSubIssues(ctx, &issue.ID)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if b, ok := briefOf(sub); ok {
			out.SubIssues = append(out.SubIssues, b)
		}
	}

	relations, err := s.q.ListRelationsForIssue(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	otherIDs := make([]uuid.UUID, 0, len(relations))
	for _, rel := range relations {
		otherIDs = append(otherIDs, rel.IssueID, rel.RelatedIssueID)
	}
	others, err := s.q.GetIssuesByIDs(ctx, otherIDs)
	if err != nil {
		return nil, err
	}
	for _, rel := range relations {
		outgoing := rel.IssueID == issue.ID
		otherID := rel.IssueID
		if outgoing {
			otherID = rel.RelatedIssueID
		}
		i := slices.IndexFunc(others, func(o dbgen.Issue) bool { return o.ID == otherID })
		if i < 0 {
			continue
		}
		b, ok := briefOf(others[i])
		if !ok {
			continue
		}
		phrase := "related to"
		switch {
		case rel.Type == "blocks" && outgoing:
			phrase = "blocks"
		case rel.Type == "blocks":
			phrase = "blocked by"
		case rel.Type == "duplicate" && outgoing:
			phrase = "duplicate of"
		case rel.Type == "duplicate":
			phrase = "duplicated by"
		}
		out.Relations = append(out.Relations, relation{Relation: phrase, brief: b})
	}

	prs, err := s.q.ListPullRequestsForIssue(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	for _, pr := range prs {
		out.PullRequests = append(out.PullRequests, pullRequest{Title: pr.Title, URL: pr.Url, Status: pr.Status, Branch: pr.Branch})
	}
	// Customers' requests carry their addresses; guests don't get them.
	if w.role != roleGuest {
		requests, err := s.q.ListCustomerRequestsForIssue(ctx, &issue.ID)
		if err != nil {
			return nil, err
		}
		for _, req := range requests {
			out.CustomerRequests = append(out.CustomerRequests, request{Title: req.Title, Requester: req.RequesterEmail, Status: req.Status, Votes: req.VoteCount})
		}
	}
	seconds, err := s.q.IssueTimeInProgress(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	out.MinutesInProgress = seconds / 60

	comments, err := s.q.ListCommentsForIssue(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	if len(comments) > mcpMaxComments {
		out.EarlierComments = len(comments) - mcpMaxComments
		comments = comments[out.EarlierComments:]
	}
	for _, c := range comments {
		author := w.userName(c.UserID)
		if author == "" {
			author = "someone who has left"
		}
		out.Comments = append(out.Comments, comment{Author: author, At: c.CreatedAt.UTC().Format(time.RFC3339), Body: c.Body})
	}
	return out, nil
}

// --- create_issue ----------------------------------------------------------------

func (s *Server) toolCreateIssue(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Team        string   `json:"team"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Status      string   `json:"status"`
		Priority    string   `json:"priority"`
		Assignee    string   `json:"assignee"`
		Labels      []string `json:"labels"`
		Project     string   `json:"project"`
		Parent      string   `json:"parent"`
		DueDate     string   `json:"dueDate"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	team, err := w.findTeam(in.Team)
	if err != nil {
		return nil, err
	}
	// A map, so only what was given is sent.
	body := map[string]any{"id": uuid.New(), "title": in.Title}
	if in.Description != "" {
		body["description"] = in.Description
	}
	if in.Status != "" {
		state, err := w.findState(team, in.Status)
		if err != nil {
			return nil, err
		}
		body["stateId"] = state.ID
	}
	if in.Priority != "" {
		priority, err := parsePriority(in.Priority)
		if err != nil {
			return nil, err
		}
		body["priority"] = priority
	}
	if in.Assignee != "" {
		user, err := w.findUser(in.Assignee)
		if err != nil {
			return nil, err
		}
		body["assigneeId"] = user.ID
	}
	if len(in.Labels) > 0 {
		ids, err := w.findLabels(in.Labels)
		if err != nil {
			return nil, err
		}
		body["labelIds"] = ids
	}
	if in.Project != "" {
		project, err := w.findProject(in.Project)
		if err != nil {
			return nil, err
		}
		body["projectId"] = project.ID
	}
	if in.Parent != "" {
		parent, _, err := s.findIssue(ctx, w, in.Parent)
		if err != nil {
			return nil, err
		}
		body["parentId"] = parent.ID
	}
	if in.DueDate != "" {
		day, err := parseDay("dueDate", in.DueDate)
		if err != nil {
			return nil, err
		}
		body["dueDate"] = day.Format(time.DateOnly)
	}
	var result api.IssueMutationResult
	err = s.callAPI(r, body, &result, func(rw http.ResponseWriter, req *http.Request) { s.CreateIssue(rw, req, team.ID) })
	if err != nil {
		return nil, err
	}
	return s.issueAfter(ctx, w, result.Issue.Id)
}

// issueAfter reads an issue back after a change, so the answer is what is
// now stored rather than what was asked for.
func (s *Server) issueAfter(ctx context.Context, w *mcpWorld, id uuid.UUID) (any, error) {
	issue, err := s.q.GetIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	return w.issue(issue), nil
}

// --- update_issue ----------------------------------------------------------------

// isNone reports whether an argument asks for a field to be cleared.
func isNone(arg string) bool {
	return strings.EqualFold(strings.TrimSpace(arg), "none")
}

func (s *Server) toolUpdateIssue(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Ref          string   `json:"ref"`
		Title        *string  `json:"title"`
		Description  *string  `json:"description"`
		Status       string   `json:"status"`
		Priority     string   `json:"priority"`
		Assignee     string   `json:"assignee"`
		AddLabels    []string `json:"addLabels"`
		RemoveLabels []string `json:"removeLabels"`
		Project      string   `json:"project"`
		Parent       string   `json:"parent"`
		DueDate      string   `json:"dueDate"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	issue, team, err := s.findIssue(ctx, w, in.Ref)
	if err != nil {
		return nil, err
	}
	// A map, because clearing a field is sending null for it.
	body := map[string]any{}
	if in.Title != nil {
		body["title"] = *in.Title
	}
	if in.Description != nil {
		body["description"] = *in.Description
	}
	if in.Status != "" {
		state, err := w.findState(team, in.Status)
		if err != nil {
			return nil, err
		}
		body["stateId"] = state.ID
	}
	if in.Priority != "" {
		priority, err := parsePriority(in.Priority)
		if err != nil {
			return nil, err
		}
		body["priority"] = priority
	}
	switch {
	case in.Assignee == "":
	case isNone(in.Assignee):
		body["assigneeId"] = nil
	default:
		user, err := w.findUser(in.Assignee)
		if err != nil {
			return nil, err
		}
		body["assigneeId"] = user.ID
	}
	if len(in.AddLabels) > 0 {
		if body["addLabelIds"], err = w.findLabels(in.AddLabels); err != nil {
			return nil, err
		}
	}
	if len(in.RemoveLabels) > 0 {
		if body["removeLabelIds"], err = w.findLabels(in.RemoveLabels); err != nil {
			return nil, err
		}
	}
	switch {
	case in.Project == "":
	case isNone(in.Project):
		body["projectId"] = nil
	default:
		project, err := w.findProject(in.Project)
		if err != nil {
			return nil, err
		}
		body["projectId"] = project.ID
	}
	switch {
	case in.Parent == "":
	case isNone(in.Parent):
		body["parentId"] = nil
	default:
		parent, _, err := s.findIssue(ctx, w, in.Parent)
		if err != nil {
			return nil, err
		}
		body["parentId"] = parent.ID
	}
	switch {
	case in.DueDate == "":
	case isNone(in.DueDate):
		body["dueDate"] = nil
	default:
		day, err := parseDay("dueDate", in.DueDate)
		if err != nil {
			return nil, err
		}
		body["dueDate"] = day.Format(time.DateOnly)
	}
	if len(body) == 0 {
		return nil, mcp.Fail("Nothing to change: give at least one field besides ref.")
	}
	err = s.callAPI(r, body, nil, func(rw http.ResponseWriter, req *http.Request) { s.UpdateIssue(rw, req, issue.ID) })
	if err != nil {
		return nil, err
	}
	return s.issueAfter(ctx, w, issue.ID)
}

// --- add_comment, log_time ---------------------------------------------------------

func (s *Server) toolAddComment(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Ref  string `json:"ref"`
		Body string `json:"body"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	issue, _, err := s.findIssue(ctx, w, in.Ref)
	if err != nil {
		return nil, err
	}
	body := api.CreateCommentRequest{Id: uuid.New(), Body: in.Body}
	err = s.callAPI(r, body, nil, func(rw http.ResponseWriter, req *http.Request) { s.CreateComment(rw, req, issue.ID) })
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("Commented on %s.", w.ref(issue)), nil
}

func (s *Server) toolLogTime(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Ref     string `json:"ref"`
		Minutes int32  `json:"minutes"`
		Note    string `json:"note"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	if in.Minutes <= 0 {
		return nil, mcp.Fail("minutes must be more than 0")
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	issue, _, err := s.findIssue(ctx, w, in.Ref)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	seconds := in.Minutes * 60
	body := api.CreateTimeEntryRequest{
		Id:        uuid.New(),
		StartedAt: now.Add(-time.Duration(seconds) * time.Second),
		EndedAt:   &now,
		Seconds:   &seconds,
		Note:      &in.Note,
	}
	err = s.callAPI(r, body, nil, func(rw http.ResponseWriter, req *http.Request) { s.CreateTimeEntry(rw, req, issue.ID) })
	if err != nil {
		return nil, err
	}
	after, err := s.q.GetIssue(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("Logged %d minutes on %s. Total logged on it: %d minutes.", in.Minutes, w.ref(issue), after.LoggedSeconds/60), nil
}

// --- Projects ----------------------------------------------------------------------

func (s *Server) toolGetProject(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Project string `json:"project"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	project, err := w.findProject(in.Project)
	if err != nil {
		return nil, err
	}
	type update struct {
		Health string `json:"health"`
		Author string `json:"author,omitempty"`
		At     string `json:"at"`
		Body   string `json:"body,omitempty"`
	}
	out := struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Description string `json:"description,omitempty"`
		Lead        string `json:"lead,omitempty"`
		Start       string `json:"start,omitempty"`
		Target      string `json:"target,omitempty"`
		// Health is the newest update's: onTrack, atRisk or offTrack.
		Health string `json:"health,omitempty"`
		// Counted over the teams you can see.
		OpenIssues      int32        `json:"openIssues"`
		CompletedIssues int32        `json:"completedIssues"`
		WaitsOn         []string     `json:"waitsOn,omitempty"`
		HoldsUp         []string     `json:"holdsUp,omitempty"`
		Updates         []update     `json:"updates,omitempty"`
		Forecast        api.Forecast `json:"forecast"`
		URL             string       `json:"url"`
	}{
		Name:        project.Name,
		Status:      project.Status,
		Description: project.Description,
		Lead:        w.userName(project.LeadID),
		URL:         fmt.Sprintf("%s/%s/project/%s", w.appURL, w.ws.UrlKey, project.ID),
	}
	if project.StartDate != nil {
		out.Start = project.StartDate.Format(time.DateOnly)
	}
	if project.TargetDate != nil {
		out.Target = project.TargetDate.Format(time.DateOnly)
	}
	scope, err := s.q.ProjectScope(ctx, dbgen.ProjectScopeParams{ProjectID: &project.ID, TeamIds: w.teamIDs})
	if err != nil {
		return nil, err
	}
	out.OpenIssues, out.CompletedIssues = scope.Remaining, scope.Completed

	dependencies, err := s.q.ListDependenciesOfProject(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range dependencies {
		if d.ProjectID == project.ID {
			out.WaitsOn = append(out.WaitsOn, w.projectName(&d.DependsOnID))
		} else {
			out.HoldsUp = append(out.HoldsUp, w.projectName(&d.ProjectID))
		}
	}
	updates, err := s.q.ListUpdatesOfProject(ctx, dbgen.ListUpdatesOfProjectParams{ProjectID: project.ID, Limit: 5})
	if err != nil {
		return nil, err
	}
	for i, u := range updates {
		if i == 0 {
			out.Health = u.Health
		}
		out.Updates = append(out.Updates, update{Health: u.Health, Author: w.userName(u.UserID), At: u.CreatedAt.UTC().Format(time.DateOnly), Body: u.Body})
	}
	err = s.callAPI(r, nil, &out.Forecast, func(rw http.ResponseWriter, req *http.Request) {
		s.GetProjectForecast(rw, req, project.ID, api.GetProjectForecastParams{})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) toolPostProjectUpdate(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Project string `json:"project"`
		Health  string `json:"health"`
		Body    string `json:"body"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	project, err := w.findProject(in.Project)
	if err != nil {
		return nil, err
	}
	// "on track" and "on_track" are as good as "onTrack".
	health := strings.ToLower(strings.NewReplacer(" ", "", "_", "", "-", "").Replace(in.Health))
	i := slices.IndexFunc(projectHealths, func(h api.ProjectHealth) bool { return strings.ToLower(string(h)) == health })
	if i < 0 {
		return nil, mcp.Fail("health is one of: onTrack, atRisk, offTrack")
	}
	body := api.CreateProjectUpdateRequest{Id: uuid.New(), Health: projectHealths[i], Body: &in.Body}
	err = s.callAPI(r, body, nil, func(rw http.ResponseWriter, req *http.Request) { s.CreateProjectUpdate(rw, req, project.ID) })
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("Posted: %s is %s.", project.Name, projectHealths[i]), nil
}

// --- team_insights -------------------------------------------------------------------

func (s *Server) toolTeamInsights(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Team string `json:"team"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	team, err := w.findTeam(in.Team)
	if err != nil {
		return nil, err
	}
	// The numbers are the ones the Insights page shows; durations are in days.
	var insights json.RawMessage
	err = s.callAPI(r, nil, &insights, func(rw http.ResponseWriter, req *http.Request) { s.GetTeamInsights(rw, req, team.ID) })
	if err != nil {
		return nil, err
	}
	return map[string]any{"team": team.Key, "durationsIn": "days", "insights": insights}, nil
}

// --- list_customer_requests ------------------------------------------------------------

func (s *Server) toolCustomerRequests(ctx context.Context, r *http.Request, args json.RawMessage) (any, error) {
	var in struct {
		Status string `json:"status"`
	}
	if err := mcp.Decode(args, &in); err != nil {
		return nil, err
	}
	w, err := s.mcpWorld(ctx, r)
	if err != nil {
		return nil, err
	}
	if w.role == roleGuest {
		return nil, mcp.Fail("Guests don't see customer requests.")
	}
	rows, err := s.q.ListCustomerRequests(ctx, w.ws.ID)
	if err != nil {
		return nil, err
	}
	type request struct {
		Title     string `json:"title"`
		Body      string `json:"body,omitempty"`
		Requester string `json:"requester"`
		Status    string `json:"status"`
		Votes     int32  `json:"votes"`
		// Issue is the issue that would deliver it, if it is tied to one you can see.
		Issue    string `json:"issue,omitempty"`
		Response string `json:"response,omitempty"`
		Created  string `json:"created"`
	}
	linked := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if row.IssueID != nil {
			linked = append(linked, *row.IssueID)
		}
	}
	issues, err := s.q.GetIssuesByIDs(ctx, linked)
	if err != nil {
		return nil, err
	}
	all := strings.EqualFold(in.Status, "all")
	out := struct {
		Requests []request `json:"requests"`
	}{Requests: []request{}}
	for _, row := range rows {
		if !all && (row.Status == "shipped" || row.Status == "declined") {
			continue
		}
		entry := request{
			Title: row.Title, Body: row.Body, Requester: row.RequesterEmail, Status: row.Status,
			Votes: row.VoteCount, Response: row.Response, Created: row.CreatedAt.UTC().Format(time.DateOnly),
		}
		if row.IssueID != nil {
			i := slices.IndexFunc(issues, func(o dbgen.Issue) bool { return o.ID == *row.IssueID })
			if i >= 0 {
				if _, visible := w.team(issues[i].TeamID); visible {
					entry.Issue = w.ref(issues[i])
				}
			}
		}
		out.Requests = append(out.Requests, entry)
	}
	slices.SortStableFunc(out.Requests, func(a, b request) int { return int(b.Votes - a.Votes) })
	return out, nil
}
