package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Linear imports a Linear workspace through its GraphQL API. The two models
// are close: teams, statuses of the same six kinds, cycles, projects with
// milestones, sub-issues and relations all carry over as they are.
type Linear struct {
	http     *http.Client
	endpoint string
	key      string
	// token is set instead of key when Linear is read through a connection.
	token    TokenFunc
	teams    []string // team IDs to import; empty means all
	progress func(string)
}

const linearEndpoint = "https://api.linear.app/graphql"

// Linear pages. Issues are fetched with their comments, relations and
// history nested, so a page is kept small enough to stay under Linear's
// limit on how much one query may ask for.
const (
	linearPage       = 50
	linearIssuePage  = 25
	linearNestedPage = 25
)

// NewLinear makes a source from a personal API key. teams limits the import
// to those team IDs.
func NewLinear(client *http.Client, apiKey string, teams []string) (*Linear, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, userError("Enter a Linear API key (Linear → Settings → Security & access → Personal API keys).")
	}
	return &Linear{http: client, endpoint: linearEndpoint, key: apiKey, teams: teams, progress: func(string) {}}, nil
}

// NewLinearConnected makes a source that reads Linear with the access token
// of a connected account.
func NewLinearConnected(client *http.Client, token TokenFunc, teams []string) *Linear {
	return &Linear{http: client, endpoint: linearEndpoint, token: token, teams: teams, progress: func(string) {}}
}

// query runs a GraphQL query and decodes its data into out.
func (l *Linear) query(ctx context.Context, query string, variables map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	renewed := false
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		// A personal key goes as it is; a connection's token says Bearer.
		authorization, bearer := l.key, ""
		if l.token != nil {
			if bearer, err = l.token(ctx, ""); err != nil {
				return err
			}
			authorization = "Bearer " + bearer
		}
		req.Header.Set("Authorization", authorization)
		res, err := l.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < 3 {
				if err := sleep(ctx, time.Duration(attempt)*2*time.Second); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("Linear could not be reached: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
		res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("Linear: reading the answer: %w", readErr)
		}
		// A token that ran out part way: once, try with a newer one.
		if res.StatusCode == http.StatusUnauthorized && l.token != nil && !renewed {
			renewed = true
			if fresh, err := l.token(ctx, bearer); err == nil && fresh != bearer {
				continue
			}
		}
		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message    string `json:"message"`
				Extensions struct {
					Code string `json:"code"`
				} `json:"extensions"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(data, &envelope)

		limited := res.StatusCode == http.StatusTooManyRequests
		for _, e := range envelope.Errors {
			limited = limited || e.Extensions.Code == "RATELIMITED"
		}
		if limited {
			// Linear says when the hour's allowance refills, in milliseconds.
			wait := time.Minute
			for _, name := range []string{"X-RateLimit-Requests-Reset", "X-RateLimit-Complexity-Reset"} {
				if at, err := strconv.ParseInt(res.Header.Get(name), 10, 64); err == nil && at > 0 {
					if until := time.Until(time.UnixMilli(at)); until > wait {
						wait = until + time.Second
					}
				}
			}
			if wait > maxRateLimitWait || attempt > 8 {
				return userError("Linear is limiting how fast this key may read. Try again later.")
			}
			l.progress(fmt.Sprintf("Waiting for Linear to allow more requests (until %s)", time.Now().Add(wait).UTC().Format("15:04 UTC")))
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		switch {
		case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
			return userError("Linear refused the API key. Check it and try again.")
		case res.StatusCode >= 500 && attempt < 4:
			if err := sleep(ctx, time.Duration(attempt)*3*time.Second); err != nil {
				return err
			}
			continue
		case len(envelope.Errors) > 0:
			if envelope.Errors[0].Extensions.Code == "AUTHENTICATION_ERROR" {
				return userError("Linear refused the API key. Check it and try again.")
			}
			return fmt.Errorf("Linear answered with an error: %s", clip(envelope.Errors[0].Message, 300))
		case res.StatusCode >= 400:
			return fmt.Errorf("Linear answered %d: %s", res.StatusCode, clip(strings.TrimSpace(string(data)), 300))
		}
		return json.Unmarshal(envelope.Data, out)
	}
}

type lnPage struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type lnRef struct {
	ID string `json:"id"`
}

type lnTeam struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	IssueCount int    `json:"issueCount"`
	States     struct {
		Nodes []struct {
			ID       string  `json:"id"`
			Name     string  `json:"name"`
			Type     string  `json:"type"`
			Color    string  `json:"color"`
			Position float64 `json:"position"`
		} `json:"nodes"`
	} `json:"states"`
}

func (l *Linear) wanted(teamID string) bool {
	return len(l.teams) == 0 || containsString(l.teams, teamID)
}

// origin is the Linear workspace, by the name in its address.
func (l *Linear) organization(ctx context.Context) (origin, label string, err error) {
	var out struct {
		Organization struct {
			Name   string `json:"name"`
			URLKey string `json:"urlKey"`
		} `json:"organization"`
	}
	if err := l.query(ctx, `query { organization { name urlKey } }`, nil, &out); err != nil {
		return "", "", err
	}
	return "linear:" + strings.ToLower(out.Organization.URLKey), out.Organization.Name + " (Linear)", nil
}

func (l *Linear) allTeams(ctx context.Context) ([]lnTeam, error) {
	var teams []lnTeam
	for cursor := (*string)(nil); ; {
		var out struct {
			Teams struct {
				Nodes    []lnTeam `json:"nodes"`
				PageInfo lnPage   `json:"pageInfo"`
			} `json:"teams"`
		}
		err := l.query(ctx, `query($first: Int!, $after: String) {
			teams(first: $first, after: $after) {
				nodes { id key name issueCount states { nodes { id name type color position } } }
				pageInfo { hasNextPage endCursor }
			}
		}`, map[string]any{"first": linearPage, "after": cursor}, &out)
		if err != nil {
			return nil, err
		}
		teams = append(teams, out.Teams.Nodes...)
		if !out.Teams.PageInfo.HasNextPage {
			return teams, nil
		}
		cursor = &out.Teams.PageInfo.EndCursor
	}
}

func (l *Linear) allUsers(ctx context.Context) ([]User, error) {
	var users []User
	for cursor := (*string)(nil); ; {
		var out struct {
			Users struct {
				Nodes []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					DisplayName string `json:"displayName"`
					Email       string `json:"email"`
				} `json:"nodes"`
				PageInfo lnPage `json:"pageInfo"`
			} `json:"users"`
		}
		err := l.query(ctx, `query($first: Int!, $after: String) {
			users(first: $first, after: $after, includeDisabled: true) {
				nodes { id name displayName email }
				pageInfo { hasNextPage endCursor }
			}
		}`, map[string]any{"first": linearPage, "after": cursor}, &out)
		if err != nil {
			return nil, err
		}
		for _, u := range out.Users.Nodes {
			name := u.Name
			if name == "" {
				name = u.DisplayName
			}
			users = append(users, User{ID: u.ID, Email: u.Email, Name: name})
		}
		if !out.Users.PageInfo.HasNextPage {
			return users, nil
		}
		cursor = &out.Users.PageInfo.EndCursor
	}
}

func (l *Linear) Preview(ctx context.Context) (Preview, error) {
	origin, label, err := l.organization(ctx)
	if err != nil {
		return Preview{}, err
	}
	teams, err := l.allTeams(ctx)
	if err != nil {
		return Preview{}, err
	}
	users, err := l.allUsers(ctx)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Origin: origin, Label: label, Teams: []PreviewTeam{}, Users: users}
	for _, t := range teams {
		p.Teams = append(p.Teams, PreviewTeam{ID: t.ID, Key: teamKey(t.Key, t.Name), Name: t.Name, Issues: t.IssueCount})
	}
	return p, nil
}

func (l *Linear) Fetch(ctx context.Context, emit func(Bundle) error, progress func(string)) error {
	l.progress = progress
	progress("Reading teams, people and projects from Linear")

	teams, err := l.allTeams(ctx)
	if err != nil {
		return err
	}
	head := Bundle{}
	var teamIDs []string
	for _, t := range teams {
		if !l.wanted(t.ID) {
			continue
		}
		team := Team{ID: t.ID, Key: t.Key, Name: t.Name}
		// Workflow order: by kind, then by Linear's own position.
		kindOrder := map[string]int{KindTriage: 0, KindBacklog: 1, KindUnstarted: 2, KindStarted: 3, KindCompleted: 4, KindCanceled: 5}
		states := t.States.Nodes
		for i := range states {
			if _, known := kindOrder[states[i].Type]; !known {
				states[i].Type = KindCanceled // "duplicate" and anything newer: a way of closing
			}
		}
		sort.SliceStable(states, func(i, j int) bool {
			a, b := states[i], states[j]
			if kindOrder[a.Type] != kindOrder[b.Type] {
				return kindOrder[a.Type] < kindOrder[b.Type]
			}
			return a.Position < b.Position
		})
		for _, s := range states {
			team.States = append(team.States, State{ID: s.ID, Name: s.Name, Kind: s.Type, Color: s.Color})
		}
		head.Teams = append(head.Teams, team)
		teamIDs = append(teamIDs, t.ID)
	}
	if len(head.Teams) == 0 {
		return userError("None of the chosen teams exist in that Linear workspace any more.")
	}
	if head.Users, err = l.allUsers(ctx); err != nil {
		return err
	}
	if head.Labels, err = l.labels(ctx); err != nil {
		return err
	}
	if head.Projects, err = l.projects(ctx); err != nil {
		return err
	}
	if head.Cycles, err = l.cycles(ctx, teamIDs); err != nil {
		return err
	}
	if err := emit(head); err != nil {
		return err
	}
	return l.issues(ctx, teamIDs, emit)
}

func (l *Linear) labels(ctx context.Context) ([]Label, error) {
	var labels []Label
	for cursor := (*string)(nil); ; {
		var out struct {
			IssueLabels struct {
				Nodes []struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Color string `json:"color"`
				} `json:"nodes"`
				PageInfo lnPage `json:"pageInfo"`
			} `json:"issueLabels"`
		}
		err := l.query(ctx, `query($first: Int!, $after: String) {
			issueLabels(first: $first, after: $after) { nodes { id name color } pageInfo { hasNextPage endCursor } }
		}`, map[string]any{"first": linearPage, "after": cursor}, &out)
		if err != nil {
			return nil, err
		}
		for _, label := range out.IssueLabels.Nodes {
			labels = append(labels, Label{ID: label.ID, Name: label.Name, Color: label.Color})
		}
		if !out.IssueLabels.PageInfo.HasNextPage {
			return labels, nil
		}
		cursor = &out.IssueLabels.PageInfo.EndCursor
	}
}

func (l *Linear) projects(ctx context.Context) ([]Project, error) {
	var projects []Project
	for cursor := (*string)(nil); ; {
		var out struct {
			Projects struct {
				Nodes []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					Description string `json:"description"`
					State       string `json:"state"`
					StartDate   string `json:"startDate"`
					TargetDate  string `json:"targetDate"`
					Lead        *lnRef `json:"lead"`
					Milestones  struct {
						Nodes []struct {
							ID         string `json:"id"`
							Name       string `json:"name"`
							TargetDate string `json:"targetDate"`
						} `json:"nodes"`
					} `json:"projectMilestones"`
				} `json:"nodes"`
				PageInfo lnPage `json:"pageInfo"`
			} `json:"projects"`
		}
		err := l.query(ctx, `query($first: Int!, $after: String) {
			projects(first: $first, after: $after, includeArchived: true) {
				nodes { id name description state startDate targetDate lead { id } projectMilestones { nodes { id name targetDate } } }
				pageInfo { hasNextPage endCursor }
			}
		}`, map[string]any{"first": linearNestedPage, "after": cursor}, &out)
		if err != nil {
			return nil, err
		}
		for _, p := range out.Projects.Nodes {
			project := Project{ID: p.ID, Name: p.Name, Description: p.Description, Status: p.State, StartDate: p.StartDate, TargetDate: p.TargetDate}
			if p.Lead != nil {
				project.Lead = p.Lead.ID
			}
			for _, m := range p.Milestones.Nodes {
				project.Milestones = append(project.Milestones, Milestone{ID: m.ID, Name: m.Name, TargetDate: m.TargetDate})
			}
			projects = append(projects, project)
		}
		if !out.Projects.PageInfo.HasNextPage {
			return projects, nil
		}
		cursor = &out.Projects.PageInfo.EndCursor
	}
}

func (l *Linear) cycles(ctx context.Context, teamIDs []string) ([]Cycle, error) {
	var cycles []Cycle
	for cursor := (*string)(nil); ; {
		var out struct {
			Cycles struct {
				Nodes []struct {
					ID       string    `json:"id"`
					Number   float64   `json:"number"`
					Name     string    `json:"name"`
					StartsAt time.Time `json:"startsAt"`
					EndsAt   time.Time `json:"endsAt"`
					Team     lnRef     `json:"team"`
				} `json:"nodes"`
				PageInfo lnPage `json:"pageInfo"`
			} `json:"cycles"`
		}
		err := l.query(ctx, `query($first: Int!, $after: String, $teams: [ID!]) {
			cycles(first: $first, after: $after, includeArchived: true, filter: { team: { id: { in: $teams } } }) {
				nodes { id number name startsAt endsAt team { id } }
				pageInfo { hasNextPage endCursor }
			}
		}`, map[string]any{"first": linearPage, "after": cursor, "teams": teamIDs}, &out)
		if err != nil {
			return nil, err
		}
		for _, c := range out.Cycles.Nodes {
			// A cycle ends at midnight; its last day is the day before.
			ends := c.EndsAt.UTC()
			if ends.Hour() == 0 && ends.Minute() == 0 && ends.After(c.StartsAt) {
				ends = ends.AddDate(0, 0, -1)
			}
			cycles = append(cycles, Cycle{
				ID: c.ID, Team: c.Team.ID, Number: int32(c.Number), Name: c.Name,
				StartsAt: c.StartsAt.UTC().Format(time.DateOnly), EndsAt: ends.Format(time.DateOnly),
			})
		}
		if !out.Cycles.PageInfo.HasNextPage {
			return cycles, nil
		}
		cursor = &out.Cycles.PageInfo.EndCursor
	}
}

type lnComment struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	User      *lnRef    `json:"user"`
}

type lnHistory struct {
	CreatedAt time.Time `json:"createdAt"`
	FromState *lnRef    `json:"fromState"`
	ToState   *lnRef    `json:"toState"`
}

type lnIssue struct {
	ID          string     `json:"id"`
	Number      float64    `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    float64    `json:"priority"`
	DueDate     string     `json:"dueDate"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	CanceledAt  *time.Time `json:"canceledAt"`
	ArchivedAt  *time.Time `json:"archivedAt"`
	URL         string     `json:"url"`
	State       lnRef      `json:"state"`
	Team        lnRef      `json:"team"`
	Assignee    *lnRef     `json:"assignee"`
	Creator     *lnRef     `json:"creator"`
	Project     *lnRef     `json:"project"`
	Milestone   *lnRef     `json:"projectMilestone"`
	Cycle       *lnRef     `json:"cycle"`
	Parent      *lnRef     `json:"parent"`
	Labels      struct {
		Nodes []lnRef `json:"nodes"`
	} `json:"labels"`
	Comments struct {
		Nodes    []lnComment `json:"nodes"`
		PageInfo lnPage      `json:"pageInfo"`
	} `json:"comments"`
	Relations struct {
		Nodes []struct {
			Type         string `json:"type"`
			RelatedIssue lnRef  `json:"relatedIssue"`
		} `json:"nodes"`
	} `json:"relations"`
	History struct {
		Nodes    []lnHistory `json:"nodes"`
		PageInfo lnPage      `json:"pageInfo"`
	} `json:"history"`
}

const linearIssueFields = `
	id number title description priority dueDate createdAt updatedAt startedAt completedAt canceledAt archivedAt url
	state { id } team { id } assignee { id } creator { id } project { id } projectMilestone { id } cycle { id } parent { id }
	labels(first: 20) { nodes { id } }
	comments(first: $nested) { nodes { id body createdAt user { id } } pageInfo { hasNextPage endCursor } }
	relations(first: 20) { nodes { type relatedIssue { id } } }
	history(first: $nested) { nodes { createdAt fromState { id } toState { id } } pageInfo { hasNextPage endCursor } }`

func (l *Linear) issues(ctx context.Context, teamIDs []string, emit func(Bundle) error) error {
	total := 0
	for cursor := (*string)(nil); ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var out struct {
			Issues struct {
				Nodes    []lnIssue `json:"nodes"`
				PageInfo lnPage    `json:"pageInfo"`
			} `json:"issues"`
		}
		err := l.query(ctx, `query($first: Int!, $after: String, $teams: [ID!], $nested: Int!) {
			issues(first: $first, after: $after, includeArchived: true, orderBy: createdAt, filter: { team: { id: { in: $teams } } }) {
				nodes {`+linearIssueFields+`}
				pageInfo { hasNextPage endCursor }
			}
		}`, map[string]any{"first": linearIssuePage, "after": cursor, "teams": teamIDs, "nested": linearNestedPage}, &out)
		if err != nil {
			return err
		}
		var b Bundle
		for _, src := range out.Issues.Nodes {
			issue, err := l.issue(ctx, src)
			if err != nil {
				return err
			}
			b.Issues = append(b.Issues, issue)
		}
		total += len(b.Issues)
		l.progress(fmt.Sprintf("Reading issues from Linear (%d so far)", total))
		if err := emit(b); err != nil {
			return err
		}
		if !out.Issues.PageInfo.HasNextPage {
			return nil
		}
		cursor = &out.Issues.PageInfo.EndCursor
	}
}

func (l *Linear) issue(ctx context.Context, src lnIssue) (Issue, error) {
	ref := func(r *lnRef) string {
		if r == nil {
			return ""
		}
		return r.ID
	}
	issue := Issue{
		ID: src.ID, Team: src.Team.ID, Number: int32(src.Number), Title: src.Title, Description: src.Description,
		State: src.State.ID, Priority: int(src.Priority), DueDate: src.DueDate,
		Assignee: ref(src.Assignee), Creator: ref(src.Creator), Project: ref(src.Project), Milestone: ref(src.Milestone),
		Cycle: ref(src.Cycle), Parent: ref(src.Parent),
		CreatedAt: src.CreatedAt, UpdatedAt: src.UpdatedAt, StartedAt: src.StartedAt, CompletedAt: src.CompletedAt,
		Archived: src.ArchivedAt != nil, URL: src.URL,
	}
	if issue.CompletedAt == nil {
		issue.CompletedAt = src.CanceledAt
	}
	for _, label := range src.Labels.Nodes {
		issue.Labels = append(issue.Labels, label.ID)
	}
	for _, rel := range src.Relations.Nodes {
		kind := RelRelated
		switch rel.Type {
		case "blocks":
			kind = RelBlocks
		case "duplicate":
			kind = RelDuplicate
		}
		issue.Relations = append(issue.Relations, Relation{Type: kind, Issue: rel.RelatedIssue.ID})
	}

	// Busy issues have more comments and history than came with the page.
	comments, history := src.Comments.Nodes, src.History.Nodes
	for page := src.Comments.PageInfo; page.HasNextPage; {
		var out struct {
			Issue struct {
				Comments struct {
					Nodes    []lnComment `json:"nodes"`
					PageInfo lnPage      `json:"pageInfo"`
				} `json:"comments"`
			} `json:"issue"`
		}
		err := l.query(ctx, `query($id: String!, $after: String) {
			issue(id: $id) { comments(first: 50, after: $after) { nodes { id body createdAt user { id } } pageInfo { hasNextPage endCursor } } }
		}`, map[string]any{"id": src.ID, "after": page.EndCursor}, &out)
		if err != nil {
			return issue, err
		}
		comments = append(comments, out.Issue.Comments.Nodes...)
		page = out.Issue.Comments.PageInfo
	}
	for page := src.History.PageInfo; page.HasNextPage; {
		var out struct {
			Issue struct {
				History struct {
					Nodes    []lnHistory `json:"nodes"`
					PageInfo lnPage      `json:"pageInfo"`
				} `json:"history"`
			} `json:"issue"`
		}
		err := l.query(ctx, `query($id: String!, $after: String) {
			issue(id: $id) { history(first: 50, after: $after) { nodes { createdAt fromState { id } toState { id } } pageInfo { hasNextPage endCursor } } }
		}`, map[string]any{"id": src.ID, "after": page.EndCursor}, &out)
		if err != nil {
			return issue, err
		}
		history = append(history, out.Issue.History.Nodes...)
		page = out.Issue.History.PageInfo
	}
	for _, c := range comments {
		issue.Comments = append(issue.Comments, Comment{ID: c.ID, Author: ref(c.User), Body: c.Body, CreatedAt: c.CreatedAt})
	}
	// Only the entries that changed the status matter. The first one says
	// what the issue was created as. (The loader puts them in order.)
	var earliest *lnHistory
	for i, h := range history {
		if h.ToState == nil {
			continue
		}
		issue.History = append(issue.History, StateChange{State: h.ToState.ID, At: h.CreatedAt})
		if h.FromState != nil && (earliest == nil || h.CreatedAt.Before(earliest.CreatedAt)) {
			earliest = &history[i]
		}
	}
	if earliest != nil {
		issue.History = append(issue.History, StateChange{State: earliest.FromState.ID, At: src.CreatedAt})
	}
	// Linear keeps pasted images and files as links that only the key can read.
	texts := []string{issue.Description}
	for _, c := range issue.Comments {
		texts = append(texts, c.Body)
	}
	issue.Attachments = uploadsInText("https://uploads.linear.app/", texts...)
	return issue, nil
}

func (l *Linear) Open(ctx context.Context, a Attachment) (ReadCloser, string, error) {
	api := &apiClient{http: l.http, name: "Linear", headers: map[string]string{"Authorization": l.key}, token: l.token}
	return api.open(ctx, a.URL)
}
