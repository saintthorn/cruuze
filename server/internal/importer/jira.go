package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Jira imports Jira projects, from Jira Cloud or from a Jira Server or Data
// Center. A project becomes a team with the project's statuses; sprints
// become cycles; an epic is the parent of the issues in it; the issue type
// and components become labels. Text is rewritten as Markdown.
type Jira struct {
	api  *apiClient
	base string // where the API is, no trailing slash
	// site is where people use it: the same as base, except through a
	// connection, whose requests go by way of Atlassian's own address.
	site     string
	projects []string // project keys to import; empty means all
	// Learned from the site on first use.
	cloud  bool
	prefix string // REST root: /rest/api/3 on Cloud, /rest/api/2 elsewhere
	title  string
	self   string // the ID of the user the credentials belong to
	ready  bool
	// since, when set, limits the issues read to those changed from then on.
	since time.Time
}

// ReadSince makes Fetch read only the issues changed since t.
func (j *Jira) ReadSince(t time.Time) { j.since = t }

// jiraProjectsAtOnce is how many projects are read at the same time.
const jiraProjectsAtOnce = 3

const jiraPage = 100

// canceledWords mark a "done" status or resolution that means the work was
// not done.
var canceledWords = regexp.MustCompile(`(?i)won'?t|cancel|reject|declin|invalid|duplicate|obsolete|abandon|not planned|cannot reproduce|can'?t reproduce`)

// NewJira makes a source for the site at baseURL. With an email the token
// is sent the way Jira Cloud wants it; without, as a bearer token, the way
// Server and Data Center take personal access tokens.
func NewJira(client *http.Client, baseURL, email, token string, projects []string) (*Jira, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.Contains(base, "://") && base != "" {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, userError("Enter the address of the Jira site, like https://acme.atlassian.net.")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, userError("Enter an API token for the Jira account.")
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	if email = strings.TrimSpace(email); email != "" {
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
	}
	keys := make([]string, 0, len(projects))
	for _, key := range projects {
		keys = append(keys, strings.ToUpper(strings.TrimSpace(key)))
	}
	return &Jira{api: &apiClient{http: client, name: "Jira", headers: headers}, base: base, site: base, projects: keys}, nil
}

// jiraGateway is where a connected account's requests to a Jira Cloud site go.
const jiraGateway = "https://api.atlassian.com/ex/jira/"

// NewJiraConnected makes a source for a Jira Cloud site read with the access
// token of a connected account. cloudID is Atlassian's ID of the site and
// siteURL its address, like https://acme.atlassian.net.
func NewJiraConnected(client *http.Client, cloudID, siteURL string, token TokenFunc, projects []string) (*Jira, error) {
	site := strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if u, err := url.Parse(site); err != nil || u.Scheme != "https" || u.Host == "" || cloudID == "" {
		return nil, userError("The Jira connection doesn't say which site it is for. Connect again.")
	}
	keys := make([]string, 0, len(projects))
	for _, key := range projects {
		keys = append(keys, strings.ToUpper(strings.TrimSpace(key)))
	}
	api := &apiClient{http: client, name: "Jira", headers: map[string]string{}, token: token}
	return &Jira{api: api, base: jiraGateway + url.PathEscape(cloudID), site: site, projects: keys}, nil
}

func (j *Jira) origin() string {
	u, _ := url.Parse(j.site)
	return "jira:" + strings.ToLower(u.Host+strings.TrimRight(u.Path, "/"))
}

// connect learns which kind of Jira this is, which decides the API version
// and with it the format text arrives in.
func (j *Jira) connect(ctx context.Context) error {
	if j.ready {
		return nil
	}
	var info struct {
		DeploymentType string `json:"deploymentType"`
		ServerTitle    string `json:"serverTitle"`
	}
	if _, err := j.api.get(ctx, j.base+"/rest/api/2/serverInfo", &info); err != nil {
		return err
	}
	// serverInfo answers anyone; whether the credentials work shows on "myself".
	var myself jiraUser
	if _, err := j.api.get(ctx, j.base+"/rest/api/2/myself", &myself); err != nil {
		return err
	}
	j.self = myself.id()
	j.cloud = strings.EqualFold(info.DeploymentType, "Cloud")
	j.prefix = j.base + "/rest/api/2"
	if j.cloud {
		j.prefix = j.base + "/rest/api/3"
	}
	j.title = info.ServerTitle
	if j.title == "" || j.title == "Jira" {
		u, _ := url.Parse(j.site)
		j.title = u.Host
	}
	j.ready = true
	return nil
}

type jiraProject struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

func (j *Jira) wanted(key string) bool {
	return len(j.projects) == 0 || containsString(j.projects, key)
}

func (j *Jira) allProjects(ctx context.Context) ([]jiraProject, error) {
	var projects []jiraProject
	if !j.cloud {
		_, err := j.api.get(ctx, j.prefix+"/project", &projects)
		return projects, err
	}
	for start := 0; ; start += 50 {
		var page struct {
			Values []jiraProject `json:"values"`
			IsLast bool          `json:"isLast"`
		}
		if _, err := j.api.get(ctx, fmt.Sprintf("%s/project/search?startAt=%d&maxResults=50", j.prefix, start), &page); err != nil {
			return nil, err
		}
		projects = append(projects, page.Values...)
		if page.IsLast || len(page.Values) == 0 {
			return projects, nil
		}
	}
}

type jiraUser struct {
	AccountID    string `json:"accountId"`
	Key          string `json:"key"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
	AccountType  string `json:"accountType"`
	Active       *bool  `json:"active"`
}

// id is the account on Cloud, the user key (or name) elsewhere.
func (u *jiraUser) id() string {
	switch {
	case u == nil:
		return ""
	case u.AccountID != "":
		return u.AccountID
	case u.Key != "":
		return u.Key
	}
	return u.Name
}

// person is a Jira user as the import knows them, marked if the token is theirs.
func (j *Jira) person(u *jiraUser) User {
	person := u.user()
	person.Self = person.ID != "" && person.ID == j.self
	return person
}

func (u *jiraUser) user() User {
	name := u.DisplayName
	if name == "" {
		name = u.Name
	}
	return User{ID: u.id(), Name: name, Email: u.EmailAddress}
}

func (j *Jira) Preview(ctx context.Context) (Preview, error) {
	if err := j.connect(ctx); err != nil {
		return Preview{}, err
	}
	projects, err := j.allProjects(ctx)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Origin: j.origin(), Label: j.title, Teams: []PreviewTeam{}, Users: []User{}}
	for _, project := range projects {
		entry := PreviewTeam{ID: project.Key, Key: teamKey(project.Key, project.Name), Name: project.Name, Issues: -1}
		// Counting costs a request per project; worth it for a handful.
		if len(projects) <= 25 {
			entry.Issues = j.count(ctx, project.Key)
		}
		p.Teams = append(p.Teams, entry)
	}

	// The people, for matching. Jira Cloud mostly hides their addresses, so
	// they are matched by hand; not being able to list them is not fatal.
	path := "/user/search?username=.&"
	if j.cloud {
		path = "/users/search?"
	}
	for start := 0; start < 2000; start += 200 {
		var page []jiraUser
		if _, err := j.api.get(ctx, fmt.Sprintf("%s%sstartAt=%d&maxResults=200", j.prefix, path, start), &page); err != nil {
			break
		}
		for i := range page {
			u := &page[i]
			if (u.AccountType != "" && u.AccountType != "atlassian") || (u.Active != nil && !*u.Active) {
				continue
			}
			p.Users = append(p.Users, j.person(u))
		}
		if len(page) < 200 {
			break
		}
	}
	return p, nil
}

func jiraJQL(key string) string {
	return fmt.Sprintf(`project = "%s" ORDER BY created ASC`, key)
}

// jiraJQLSince is jiraJQL for the issues changed since a time. The time is
// given as minutes ago: a date would be read in the account's time zone.
func jiraJQLSince(key string, since, now time.Time) string {
	minutes := int(now.Sub(since).Minutes()) + 1
	return fmt.Sprintf(`project = "%s" AND updated >= "-%dm" ORDER BY created ASC`, key, minutes)
}

// count is how many issues a project has, or -1 if Jira won't say.
func (j *Jira) count(ctx context.Context, key string) int {
	if j.cloud {
		var out struct {
			Count int `json:"count"`
		}
		if _, err := j.api.call(ctx, http.MethodPost, j.prefix+"/search/approximate-count", map[string]string{"jql": fmt.Sprintf(`project = "%s"`, key)}, &out); err != nil {
			return -1
		}
		return out.Count
	}
	var out struct {
		Total int `json:"total"`
	}
	query := url.Values{"jql": {fmt.Sprintf(`project = "%s"`, key)}, "maxResults": {"0"}}
	if _, err := j.api.get(ctx, j.prefix+"/search?"+query.Encode(), &out); err != nil {
		return -1
	}
	return out.Total
}

// jiraFields are the custom fields that matter, which every site numbers
// differently.
type jiraFields struct {
	sprint   string
	epicLink string
}

func (j *Jira) fields(ctx context.Context) (jiraFields, error) {
	var all []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Schema struct {
			Custom string `json:"custom"`
		} `json:"schema"`
	}
	var found jiraFields
	if _, err := j.api.get(ctx, j.prefix+"/field", &all); err != nil {
		return found, err
	}
	for _, f := range all {
		switch {
		case f.Schema.Custom == "com.pyxis.greenhopper.jira:gh-sprint":
			found.sprint = f.ID
		case f.Schema.Custom == "com.pyxis.greenhopper.jira:gh-epic-link":
			found.epicLink = f.ID
		}
	}
	return found, nil
}

type jiraStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	StatusCategory struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

// kind is the kind of status this is here.
func (s jiraStatus) kind() string {
	switch s.StatusCategory.Key {
	case "done":
		if canceledWords.MatchString(s.Name) {
			return KindCanceled
		}
		return KindCompleted
	case "indeterminate":
		return KindStarted
	}
	if strings.Contains(strings.ToLower(s.Name), "backlog") {
		return KindBacklog
	}
	return KindUnstarted
}

// Status IDs are shared between Jira projects; statuses here belong to a
// team. So a status is named by its project as well.
func jiraStateID(project, statusID string) string { return project + ":" + statusID }

// jiraCanceledID is a status of our own making, for issues that Jira closed
// with a resolution like "Won't Do" while leaving them in a plain Done.
func jiraCanceledID(project string) string { return project + ":canceled" }

func (j *Jira) team(ctx context.Context, project jiraProject) (Team, error) {
	var types []struct {
		Statuses []jiraStatus `json:"statuses"`
	}
	if _, err := j.api.get(ctx, fmt.Sprintf("%s/project/%s/statuses", j.prefix, url.PathEscape(project.Key)), &types); err != nil {
		return Team{}, err
	}
	team := Team{ID: project.Key, Key: project.Key, Name: project.Name}
	seen := map[string]bool{}
	for _, issueType := range types {
		for _, status := range issueType.Statuses {
			if seen[status.ID] {
				continue
			}
			seen[status.ID] = true
			team.States = append(team.States, State{ID: jiraStateID(project.Key, status.ID), Name: status.Name, Kind: status.kind()})
		}
	}
	order := map[string]int{KindBacklog: 0, KindUnstarted: 1, KindStarted: 2, KindCompleted: 3, KindCanceled: 4}
	sort.SliceStable(team.States, func(a, b int) bool { return order[team.States[a].Kind] < order[team.States[b].Kind] })
	team.States = append(team.States, State{ID: jiraCanceledID(project.Key), Name: "Canceled", Kind: KindCanceled})
	return team, nil
}

// sprints lists a project's sprints as cycles, numbered in the order they
// ran. Jira without boards, or boards without sprints, has none.
func (j *Jira) sprints(ctx context.Context, project string) ([]Cycle, map[string]string) {
	type sprint struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	}
	var boards struct {
		Values []struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		} `json:"values"`
	}
	query := url.Values{"projectKeyOrId": {project}, "maxResults": {"50"}}
	if _, err := j.api.get(ctx, j.base+"/rest/agile/1.0/board?"+query.Encode(), &boards); err != nil {
		return nil, nil
	}
	byID := map[int]sprint{}
	for _, board := range boards.Values {
		if board.Type != "scrum" {
			continue
		}
		for start := 0; ; start += 50 {
			var page struct {
				Values []sprint `json:"values"`
				IsLast bool     `json:"isLast"`
			}
			if _, err := j.api.get(ctx, fmt.Sprintf("%s/rest/agile/1.0/board/%d/sprint?startAt=%d&maxResults=50", j.base, board.ID, start), &page); err != nil {
				break
			}
			for _, s := range page.Values {
				byID[s.ID] = s
			}
			if page.IsLast || len(page.Values) == 0 {
				break
			}
		}
	}
	var dated []sprint
	for _, s := range byID {
		if len(s.StartDate) >= len(time.DateOnly) && len(s.EndDate) >= len(time.DateOnly) {
			dated = append(dated, s)
		}
	}
	sort.Slice(dated, func(a, b int) bool {
		if dated[a].StartDate != dated[b].StartDate {
			return dated[a].StartDate < dated[b].StartDate
		}
		return dated[a].ID < dated[b].ID
	})
	var cycles []Cycle
	ids := map[string]string{} // sprint ID → cycle ID
	for i, s := range dated {
		id := fmt.Sprintf("%s:sprint:%d", project, s.ID)
		ids[strconv.Itoa(s.ID)] = id
		cycles = append(cycles, Cycle{
			ID: id, Team: project, Number: int32(i + 1), Name: s.Name,
			StartsAt: s.StartDate[:len(time.DateOnly)], EndsAt: s.EndDate[:len(time.DateOnly)],
		})
	}
	return cycles, ids
}

type jiraComment struct {
	ID      string          `json:"id"`
	Author  *jiraUser       `json:"author"`
	Body    json.RawMessage `json:"body"`
	Created string          `json:"created"`
}

type jiraHistory struct {
	Created string `json:"created"`
	Items   []struct {
		Field string `json:"field"`
		From  string `json:"from"`
		To    string `json:"to"`
	} `json:"items"`
}

type jiraIssue struct {
	Key       string                     `json:"key"`
	Fields    map[string]json.RawMessage `json:"fields"`
	Changelog struct {
		Total     int           `json:"total"`
		Histories []jiraHistory `json:"histories"`
	} `json:"changelog"`
}

// field decodes one of an issue's fields; a missing or null field leaves
// out as it was.
func (i jiraIssue) field(name string, out any) {
	if raw, ok := i.Fields[name]; ok && string(raw) != "null" {
		_ = json.Unmarshal(raw, out)
	}
}

func (j *Jira) Fetch(ctx context.Context, emit func(Bundle) error, progress func(string)) error {
	j.api.progress = progress
	progress("Connecting to Jira")
	if err := j.connect(ctx); err != nil {
		return err
	}
	fields, err := j.fields(ctx)
	if err != nil {
		return err
	}
	projects, err := j.allProjects(ctx)
	if err != nil {
		return err
	}
	var wanted []jiraProject
	for _, project := range projects {
		if j.wanted(project.Key) {
			wanted = append(wanted, project)
		}
	}
	if len(wanted) == 0 {
		return userError("None of the chosen projects exist on that Jira site, or this account can't see them.")
	}
	// A few projects at a time. Each hands over its team before its issues;
	// the first failure stops the others. Bundles and progress are still
	// handed over one at a time.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		reading sync.WaitGroup
		mu      sync.Mutex
		failed  error
		handing sync.Mutex
	)
	emitAll, progressAll := emit, progress
	emit = func(b Bundle) error {
		handing.Lock()
		defer handing.Unlock()
		return emitAll(b)
	}
	progress = func(stage string) {
		handing.Lock()
		defer handing.Unlock()
		progressAll(stage)
	}
	j.api.progress = progress
	slots := make(chan struct{}, jiraProjectsAtOnce)
	for _, project := range wanted {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		reading.Add(1)
		go func() {
			defer reading.Done()
			defer func() { <-slots }()
			if err := j.project(ctx, project, fields, emit, progress); err != nil {
				mu.Lock()
				if failed == nil {
					failed = err
				}
				mu.Unlock()
				cancel()
			}
		}()
	}
	reading.Wait()
	if failed != nil {
		return failed
	}
	return ctx.Err()
}

// project reads one project: its team and sprints, then its issues.
func (j *Jira) project(ctx context.Context, project jiraProject, fields jiraFields, emit func(Bundle) error, progress func(string)) error {
	progress("Reading " + project.Key + " from Jira")
	team, err := j.team(ctx, project)
	if err != nil {
		return err
	}
	cycles, sprintIDs := j.sprints(ctx, project.Key)
	if err := emit(Bundle{Teams: []Team{team}, Cycles: cycles}); err != nil {
		return err
	}
	return j.issues(ctx, project.Key, fields, sprintIDs, emit, progress)
}

func (j *Jira) issues(ctx context.Context, project string, fields jiraFields, sprintIDs map[string]string, emit func(Bundle) error, progress func(string)) error {
	names := []string{"summary", "description", "status", "priority", "assignee", "reporter", "labels", "components", "duedate",
		"created", "updated", "resolutiondate", "resolution", "issuetype", "parent", "issuelinks", "comment", "attachment"}
	for _, id := range []string{fields.sprint, fields.epicLink} {
		if id != "" {
			names = append(names, id)
		}
	}
	jql := jiraJQL(project)
	if !j.since.IsZero() {
		jql = jiraJQLSince(project, j.since, time.Now())
	}
	query := url.Values{"jql": {jql}, "maxResults": {strconv.Itoa(jiraPage)}, "fields": {strings.Join(names, ",")}, "expand": {"changelog"}}
	seen := map[string]bool{}
	total := 0
	for start, token := 0, ""; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var page struct {
			Issues        []jiraIssue `json:"issues"`
			Total         int         `json:"total"`
			NextPageToken string      `json:"nextPageToken"`
			IsLast        *bool       `json:"isLast"`
		}
		address := j.prefix + "/search"
		if j.cloud {
			// Jira Cloud's search pages by token rather than by position.
			address += "/jql"
			query.Del("nextPageToken")
			if token != "" {
				query.Set("nextPageToken", token)
			}
		} else {
			query.Set("startAt", strconv.Itoa(start))
		}
		if _, err := j.api.get(ctx, address+"?"+query.Encode(), &page); err != nil {
			return err
		}
		var b Bundle
		for _, src := range page.Issues {
			issue, people, err := j.issue(ctx, project, src, fields, sprintIDs)
			if err != nil {
				return err
			}
			for _, person := range people {
				if person.ID != "" && !seen[person.ID] {
					seen[person.ID] = true
					b.Users = append(b.Users, person)
				}
			}
			b.Issues = append(b.Issues, issue)
		}
		total += len(b.Issues)
		progress(fmt.Sprintf("Reading %s from Jira (%d issues so far)", project, total))
		if err := emit(b); err != nil {
			return err
		}
		start += len(page.Issues)
		token = page.NextPageToken
		done := len(page.Issues) == 0
		if j.cloud {
			done = done || token == "" || (page.IsLast != nil && *page.IsLast)
		} else {
			done = done || start >= page.Total
		}
		if done {
			return nil
		}
	}
}

// text converts a description or comment body, which is a document tree on
// Cloud and wiki markup elsewhere.
func (j *Jira) text(raw json.RawMessage) string {
	if j.cloud {
		return adfToMarkdown(raw)
	}
	var wiki string
	_ = json.Unmarshal(raw, &wiki)
	return wikiToMarkdown(wiki)
}

func jiraTime(raw string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

var (
	jiraSprintInText = regexp.MustCompile(`\bid=(\d+)`)
	jiraPriorities   = map[string]int{
		"highest": 1, "blocker": 1, "critical": 1, "urgent": 1,
		"high": 2, "major": 2,
		"medium": 3, "normal": 3,
		"low": 4, "lowest": 4, "minor": 4, "trivial": 4,
	}
)

func (j *Jira) issue(ctx context.Context, project string, src jiraIssue, fields jiraFields, sprintIDs map[string]string) (Issue, []User, error) {
	var (
		summary, due, created, updated, resolved string
		description                              json.RawMessage
		status                                   jiraStatus
		priority, resolution, issueType          struct{ Name string }
		assignee, reporter                       *jiraUser
		labels                                   []string
		components                               []struct{ Name string }
		parent                                   *struct{ Key string }
	)
	src.field("summary", &summary)
	src.field("description", &description)
	src.field("status", &status)
	src.field("priority", &priority)
	src.field("resolution", &resolution)
	src.field("issuetype", &issueType)
	src.field("assignee", &assignee)
	src.field("reporter", &reporter)
	src.field("labels", &labels)
	src.field("components", &components)
	src.field("duedate", &due)
	src.field("created", &created)
	src.field("updated", &updated)
	src.field("resolutiondate", &resolved)
	src.field("parent", &parent)

	number, _ := strconv.Atoi(src.Key[strings.LastIndex(src.Key, "-")+1:])
	issue := Issue{
		ID: src.Key, Team: project, Number: int32(number), Title: summary, Description: j.text(description),
		State: jiraStateID(project, status.ID), Priority: jiraPriorities[strings.ToLower(priority.Name)],
		Assignee: assignee.id(), Creator: reporter.id(), DueDate: due,
		CreatedAt: jiraTime(created), UpdatedAt: jiraTime(updated), URL: j.site + "/browse/" + src.Key,
	}
	var people []User
	for _, u := range []*jiraUser{assignee, reporter} {
		if u != nil {
			people = append(people, j.person(u))
		}
	}
	// Closed with a resolution that says it wasn't done, in a status that
	// doesn't: here that is canceled.
	closed := status.StatusCategory.Key == "done"
	if closed && status.kind() == KindCompleted && canceledWords.MatchString(resolution.Name) {
		issue.State = jiraCanceledID(project)
	}
	if closed {
		at := jiraTime(resolved)
		if at.IsZero() {
			at = issue.UpdatedAt
		}
		issue.CompletedAt = &at
	}
	issue.Labels = append(issue.Labels, labels...)
	for _, c := range components {
		issue.Labels = append(issue.Labels, c.Name)
	}
	if issueType.Name != "" {
		issue.Labels = append(issue.Labels, issueType.Name)
	}
	if parent != nil {
		issue.Parent = parent.Key
	} else if fields.epicLink != "" {
		src.field(fields.epicLink, &issue.Parent)
	}
	// The sprint field lists every sprint the issue has been in; the last
	// is the one it is in (or ended in).
	if fields.sprint != "" {
		var sprints []json.RawMessage
		src.field(fields.sprint, &sprints)
		for _, raw := range sprints {
			var object struct {
				ID int `json:"id"`
			}
			id := ""
			if json.Unmarshal(raw, &object) == nil && object.ID != 0 {
				id = strconv.Itoa(object.ID)
			} else if m := jiraSprintInText.FindStringSubmatch(string(raw)); m != nil {
				// Older servers describe a sprint in a string.
				id = m[1]
			}
			if cycle, ok := sprintIDs[id]; ok {
				issue.Cycle = cycle
			}
		}
	}

	var links []struct {
		Type         struct{ Name string } `json:"type"`
		OutwardIssue *struct{ Key string } `json:"outwardIssue"`
		InwardIssue  *struct{ Key string } `json:"inwardIssue"`
	}
	src.field("issuelinks", &links)
	for _, link := range links {
		name := strings.ToLower(link.Type.Name)
		switch {
		case strings.Contains(name, "block") && link.OutwardIssue != nil:
			issue.Relations = append(issue.Relations, Relation{Type: RelBlocks, Issue: link.OutwardIssue.Key})
		case strings.Contains(name, "block") && link.InwardIssue != nil:
			issue.Relations = append(issue.Relations, Relation{Type: RelBlockedBy, Issue: link.InwardIssue.Key})
		case strings.Contains(name, "duplicate") && link.OutwardIssue != nil:
			issue.Relations = append(issue.Relations, Relation{Type: RelDuplicate, Issue: link.OutwardIssue.Key})
		case strings.Contains(name, "duplicate"):
			// The other issue says it; once is enough.
		case link.OutwardIssue != nil:
			issue.Relations = append(issue.Relations, Relation{Type: RelRelated, Issue: link.OutwardIssue.Key})
		case link.InwardIssue != nil:
			issue.Relations = append(issue.Relations, Relation{Type: RelRelated, Issue: link.InwardIssue.Key})
		}
	}

	var attachments []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	src.field("attachment", &attachments)
	for _, a := range attachments {
		issue.Attachments = append(issue.Attachments, Attachment{ID: a.ID, Filename: a.Filename, URL: a.Content})
	}

	// Comments: what came with the issue, and the rest if there are more.
	var thread struct {
		Comments []jiraComment `json:"comments"`
		Total    int           `json:"total"`
	}
	src.field("comment", &thread)
	for len(thread.Comments) < thread.Total {
		var more struct {
			Comments []jiraComment `json:"comments"`
		}
		address := fmt.Sprintf("%s/issue/%s/comment?startAt=%d&maxResults=100", j.prefix, url.PathEscape(src.Key), len(thread.Comments))
		if _, err := j.api.get(ctx, address, &more); err != nil {
			return issue, nil, err
		}
		if len(more.Comments) == 0 {
			break
		}
		thread.Comments = append(thread.Comments, more.Comments...)
	}
	for _, c := range thread.Comments {
		if c.Author != nil {
			people = append(people, j.person(c.Author))
		}
		issue.Comments = append(issue.Comments, Comment{ID: c.ID, Author: c.Author.id(), Body: j.text(c.Body), CreatedAt: jiraTime(c.Created)})
	}

	// Status changes, from the change log. Cloud sends only the first
	// hundred entries with a search result.
	histories := src.Changelog.Histories
	for j.cloud && len(histories) < src.Changelog.Total {
		var more struct {
			Values []jiraHistory `json:"values"`
		}
		address := fmt.Sprintf("%s/issue/%s/changelog?startAt=%d&maxResults=100", j.prefix, url.PathEscape(src.Key), len(histories))
		if _, err := j.api.get(ctx, address, &more); err != nil {
			return issue, nil, err
		}
		if len(more.Values) == 0 {
			break
		}
		histories = append(histories, more.Values...)
	}
	var first *StateChange
	for _, h := range histories {
		at := jiraTime(h.Created)
		for _, item := range h.Items {
			if item.Field != "status" || item.To == "" {
				continue
			}
			issue.History = append(issue.History, StateChange{State: jiraStateID(project, item.To), At: at})
			if item.From != "" && (first == nil || at.Before(first.At)) {
				first = &StateChange{State: jiraStateID(project, item.From), At: at}
			}
		}
	}
	if first != nil {
		// What it was before its first change is what it was created as.
		issue.History = append(issue.History, StateChange{State: first.State, At: issue.CreatedAt})
	}
	return issue, people, nil
}

func (j *Jira) Open(ctx context.Context, a Attachment) (ReadCloser, string, error) {
	address := a.URL
	// A connection's token is only taken at Atlassian's own address.
	if j.site != j.base && strings.HasPrefix(address, j.site+"/") {
		address = j.base + address[len(j.site):]
	}
	return j.api.open(ctx, address)
}
