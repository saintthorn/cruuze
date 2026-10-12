package importer

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GitHub imports a repository's issues. A repository becomes a team; its
// milestones become projects (they have a due date and a share done, which
// is what a project is here); pull requests are left out.
type GitHub struct {
	api   *apiClient
	base  string // API root, no trailing slash
	owner string
	repo  string
	// since, when set, limits the issues and comments read to those
	// changed from then on.
	since time.Time
}

// ReadSince makes Fetch read only what changed since t.
func (g *GitHub) ReadSince(t time.Time) { g.since = t }

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

// GitHub status IDs: an issue is open, or closed one of two ways.
const (
	ghOpen       = "open"
	ghCompleted  = "completed"
	ghNotPlanned = "not_planned"
)

// NewGitHub makes a source for owner/name. token may be empty for a public
// repository. baseURL is the API root of a GitHub Enterprise Server, or
// empty for github.com.
func NewGitHub(client *http.Client, repo, token, baseURL string) (*GitHub, error) {
	repo = strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	// A pasted address is as good as owner/name.
	if i := strings.Index(repo, "github.com/"); i >= 0 {
		repo = strings.Trim(repo[i+len("github.com/"):], "/")
	}
	if !repoPattern.MatchString(repo) {
		return nil, userError(`Name the repository as owner/name, like "acme/widgets".`)
	}
	owner, name, _ := strings.Cut(repo, "/")
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://api.github.com"
	} else if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, userError("The GitHub Enterprise address should look like https://github.example.com/api/v3.")
	}
	headers := map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "cruuze-importer"}
	if token = strings.TrimSpace(token); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return &GitHub{api: &apiClient{http: client, name: "GitHub", headers: headers}, base: base, owner: owner, repo: name}, nil
}

func (g *GitHub) origin() string {
	host := "github"
	if u, err := url.Parse(g.base); err == nil && u.Host != "api.github.com" {
		host = "github:" + u.Host
	}
	return strings.ToLower(fmt.Sprintf("%s:%s/%s", host, g.owner, g.repo))
}

func (g *GitHub) repoURL(path string, query url.Values) string {
	address := fmt.Sprintf("%s/repos/%s/%s%s", g.base, url.PathEscape(g.owner), url.PathEscape(g.repo), path)
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	return address
}

type ghUser struct {
	Login string `json:"login"`
}

type ghLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ghMilestone struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	State       string  `json:"state"`
	DueOn       *string `json:"due_on"`
}

type ghIssue struct {
	Number      int          `json:"number"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	State       string       `json:"state"`
	StateReason string       `json:"state_reason"`
	User        *ghUser      `json:"user"`
	Assignee    *ghUser      `json:"assignee"`
	Labels      []ghLabel    `json:"labels"`
	Milestone   *ghMilestone `json:"milestone"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	ClosedAt    *time.Time   `json:"closed_at"`
	HTMLURL     string       `json:"html_url"`
	// Present on pull requests, which the issues API lists too.
	PullRequest *struct{} `json:"pull_request"`
}

type ghComment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      *ghUser   `json:"user"`
	IssueURL  string    `json:"issue_url"`
	CreatedAt time.Time `json:"created_at"`
}

func (g *GitHub) team() Team {
	return Team{
		ID: g.origin(), Key: g.repo, Name: g.repo,
		// GitHub has open and closed; closed says why. The loader adds an
		// in-progress status, which GitHub has no word for.
		States: []State{
			{ID: ghOpen, Name: "Todo", Kind: KindUnstarted},
			{ID: ghCompleted, Name: "Done", Kind: KindCompleted},
			{ID: ghNotPlanned, Name: "Not Planned", Kind: KindCanceled},
		},
	}
}

func (g *GitHub) Preview(ctx context.Context) (Preview, error) {
	var repo struct {
		FullName  string `json:"full_name"`
		HasIssues bool   `json:"has_issues"`
	}
	if _, err := g.api.get(ctx, g.repoURL("", nil), &repo); err != nil {
		return Preview{}, err
	}
	if !repo.HasIssues {
		return Preview{}, userError(repo.FullName + " has issues turned off.")
	}
	p := Preview{Origin: g.origin(), Label: repo.FullName, Users: []User{}}
	team := g.team()
	entry := PreviewTeam{ID: team.ID, Key: teamKey(team.Key, team.Name), Name: team.Name, Issues: -1}
	// The search API counts issues without pull requests. It has a tight
	// rate limit of its own, so a failure here is not worth stopping for.
	var found struct {
		TotalCount int `json:"total_count"`
	}
	query := url.Values{"q": {fmt.Sprintf("repo:%s/%s is:issue", g.owner, g.repo)}, "per_page": {"1"}}
	if _, err := g.api.get(ctx, g.base+"/search/issues?"+query.Encode(), &found); err == nil {
		entry.Issues = found.TotalCount
	}
	p.Teams = []PreviewTeam{entry}

	// The people who can be assigned issues: the ones worth matching.
	var assignees []ghUser
	if _, err := g.api.get(ctx, g.repoURL("/assignees", url.Values{"per_page": {"100"}}), &assignees); err == nil {
		for _, u := range assignees {
			p.Users = append(p.Users, ghPerson(u.Login))
		}
	}
	return p, nil
}

func ghPerson(login string) User {
	return User{ID: login, Name: login, GitHubLogin: login}
}

func (g *GitHub) Fetch(ctx context.Context, emit func(Bundle) error, progress func(string)) error {
	g.api.progress = progress
	progress("Reading labels and milestones from GitHub")

	head := Bundle{Teams: []Team{g.team()}}
	err := ghPages(ctx, g, g.repoURL("/labels", url.Values{"per_page": {"100"}}), func(page []ghLabel) error {
		for _, l := range page {
			head.Labels = append(head.Labels, Label{ID: l.Name, Name: l.Name, Color: l.Color})
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = ghPages(ctx, g, g.repoURL("/milestones", url.Values{"state": {"all"}, "per_page": {"100"}}), func(page []ghMilestone) error {
		for _, m := range page {
			project := Project{ID: ghMilestoneID(m.Number), Name: m.Title, Description: m.Description, Status: "started"}
			if m.State == "closed" {
				project.Status = "completed"
			}
			if m.DueOn != nil {
				project.TargetDate = *m.DueOn
			}
			head.Projects = append(head.Projects, project)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := emit(head); err != nil {
		return err
	}

	// Oldest first, so an interrupted import has the early numbers and a
	// second run adds the rest.
	seen := map[string]bool{}
	total := 0
	query := url.Values{"state": {"all"}, "sort": {"created"}, "direction": {"asc"}, "per_page": {"100"}}
	if !g.since.IsZero() {
		query.Set("since", g.since.UTC().Format(time.RFC3339))
	}
	err = ghPages(ctx, g, g.repoURL("/issues", query), func(page []ghIssue) error {
		var b Bundle
		person := func(u *ghUser) string {
			if u == nil || u.Login == "" {
				return ""
			}
			if !seen[u.Login] {
				seen[u.Login] = true
				b.Users = append(b.Users, ghPerson(u.Login))
			}
			return u.Login
		}
		for _, src := range page {
			if src.PullRequest != nil {
				continue
			}
			issue := Issue{
				ID: strconv.Itoa(src.Number), Team: g.origin(), Number: int32(src.Number),
				Title: src.Title, Description: src.Body, State: ghOpen,
				Creator: person(src.User), Assignee: person(src.Assignee),
				CreatedAt: src.CreatedAt, UpdatedAt: src.UpdatedAt, URL: src.HTMLURL,
			}
			for _, l := range src.Labels {
				issue.Labels = append(issue.Labels, l.Name)
			}
			if src.Milestone != nil {
				issue.Project = ghMilestoneID(src.Milestone.Number)
			}
			if src.State == "closed" {
				issue.State = ghCompleted
				if src.StateReason == "not_planned" || src.StateReason == "duplicate" {
					issue.State = ghNotPlanned
				}
				issue.CompletedAt = src.ClosedAt
			}
			b.Issues = append(b.Issues, issue)
		}
		total += len(b.Issues)
		progress(fmt.Sprintf("Reading issues from GitHub (%d so far)", total))
		return emit(b)
	})
	if err != nil {
		return err
	}

	// Every comment in the repository in one stream, rather than a request
	// per issue. Those on pull requests find no issue and are dropped.
	comments := 0
	query = url.Values{"sort": {"created"}, "direction": {"asc"}, "per_page": {"100"}}
	if !g.since.IsZero() {
		query.Set("since", g.since.UTC().Format(time.RFC3339))
	}
	return ghPages(ctx, g, g.repoURL("/issues/comments", query), func(page []ghComment) error {
		var b Bundle
		for _, c := range page {
			number := c.IssueURL[strings.LastIndex(c.IssueURL, "/")+1:]
			author := ""
			if c.User != nil && c.User.Login != "" {
				author = c.User.Login
				if !seen[author] {
					seen[author] = true
					b.Users = append(b.Users, ghPerson(author))
				}
			}
			b.Comments = append(b.Comments, IssueComment{Issue: number, Comment: Comment{
				ID: strconv.FormatInt(c.ID, 10), Author: author, Body: c.Body, CreatedAt: c.CreatedAt,
			}})
		}
		comments += len(page)
		progress(fmt.Sprintf("Reading comments from GitHub (%d so far)", comments))
		return emit(b)
	})
}

func ghMilestoneID(number int) string { return "milestone:" + strconv.Itoa(number) }

// ghPages reads a paged list, following GitHub's "next" links.
func ghPages[T any](ctx context.Context, g *GitHub, address string, each func([]T) error) error {
	for address != "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		var page []T
		header, err := g.api.get(ctx, address, &page)
		if err != nil {
			return err
		}
		if err := each(page); err != nil {
			return err
		}
		address = nextLink(header)
	}
	return nil
}

func (g *GitHub) Open(ctx context.Context, a Attachment) (ReadCloser, string, error) {
	return g.api.open(ctx, a.URL)
}
