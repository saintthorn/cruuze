package importer

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// csvSource is a spreadsheet of issues: the export every tracker has, and
// the way in for a server that can't reach the tracker itself. It reads the
// column names Linear, Jira and GitHub use, and keeps what a plain CSV
// import would lose: issue numbers, dates, parents.
type csvSource struct {
	file  File
	teams []string // team IDs to import; empty means all
}

// csvColumns maps a column heading (letters and digits only, lowercased) to
// what it holds. The headings are the ones the common trackers export.
var csvColumns = map[string]string{
	"issuekey": "id", "identifier": "id", "key": "id", "id": "id",
	"title": "title", "summary": "title", "name": "title", "subject": "title",
	"description": "description", "body": "description", "details": "description",
	"status": "status", "state": "status",
	"priority": "priority",
	"assignee": "assignee", "assigneeemail": "assignee", "assignedto": "assignee", "owner": "assignee",
	"creator": "creator", "reporter": "creator", "author": "creator", "createdby": "creator",
	"labels": "labels", "label": "labels", "tags": "labels", "components": "labels", "component": "labels",
	"issuetype": "labels", "type": "labels",
	"duedate": "due", "due": "due",
	"created": "created", "createdat": "created", "createddate": "created", "datecreated": "created",
	"updated": "updated", "updatedat": "updated", "lastupdated": "updated",
	"completed": "completed", "completedat": "completed", "resolved": "completed", "resolutiondate": "completed",
	"closed": "completed", "closedat": "completed", "canceled": "completed", "canceledat": "completed",
	"started": "started", "startedat": "started",
	"parent": "parent", "parentissue": "parent", "parentid": "parent", "parentkey": "parent",
	"project": "project", "projectname": "teamname", "projectkey": "teamkey",
	"team": "teamname", "teamname": "teamname", "teamkey": "teamkey",
	"archived": "archived", "archivedat": "archived",
	"url": "url", "link": "url",
}

var (
	csvIssueKey   = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*)-(\d+)$`)
	csvStarted    = regexp.MustCompile(`(?i)progress|review|doing|started|develop|testing|\bqa\b|underway`)
	csvCompleted  = regexp.MustCompile(`(?i)done|complete|closed|resolved|shipped|released|merged|fixed`)
	csvBacklog    = regexp.MustCompile(`(?i)backlog|icebox|someday`)
	csvDateLayout = []string{
		time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02 15:04:05", "2006-01-02 15:04", time.DateOnly,
		"02/Jan/06 3:04 PM", "02/Jan/06 15:04", "02/Jan/2006 3:04 PM", "Jan 2, 2006", "January 2, 2006",
		"Mon Jan 02 2006 15:04:05 GMT-0700", "1/2/2006 15:04", "1/2/2006",
	}
)

// csvTime reads a date in any of the forms trackers write them.
func csvTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	// "Wed Sep 10 2026 10:00:00 GMT+0000 (Coordinated Universal Time)"
	if i := strings.Index(raw, " ("); i > 0 {
		raw = raw[:i]
	}
	for _, layout := range csvDateLayout {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func csvHeading(raw string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, raw)
}

// FromCSV reads a CSV export into a source. name says which export it is,
// so that importing a newer copy of the same one updates the first.
// teamName is where rows go that don't say which team they are in.
func FromCSV(text, name, teamName string) (Source, error) {
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\xEF\xBB\xBF")))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	rows, err := reader.ReadAll()
	if err != nil {
		var parseErr *csv.ParseError
		if errors.As(err, &parseErr) {
			return nil, userError(fmt.Sprintf("The file isn't valid CSV (line %d).", parseErr.Line))
		}
		return nil, userError("The file isn't valid CSV.")
	}
	if len(rows) < 2 {
		return nil, userError("The file has no rows under its heading.")
	}
	// Several columns can hold the same thing (Jira repeats "Labels").
	columns := map[string][]int{}
	for i, heading := range rows[0] {
		if field, ok := csvColumns[csvHeading(heading)]; ok {
			columns[field] = append(columns[field], i)
		}
	}
	if len(columns["title"]) == 0 {
		return nil, userError(`The file needs a column of titles, headed "Title" or "Summary".`)
	}
	if name = strings.TrimSpace(name); name == "" {
		name = "upload"
	}
	if teamName = strings.TrimSpace(teamName); teamName == "" {
		teamName = "Imported"
	}

	file := File{Origin: "csv:" + strings.ToLower(name), Label: name + " (CSV)"}
	type teamInfo struct {
		team   *Team
		states map[string]*csvStatus
	}
	teams := map[string]*teamInfo{}
	var teamOrder []string
	users, projects, labels := map[string]bool{}, map[string]bool{}, map[string]bool{}

	for _, row := range rows[1:] {
		all := func(field string) []string {
			var values []string
			for _, i := range columns[field] {
				if i < len(row) && strings.TrimSpace(row[i]) != "" {
					values = append(values, strings.TrimSpace(row[i]))
				}
			}
			return values
		}
		one := func(field string) string {
			if values := all(field); len(values) > 0 {
				return values[0]
			}
			return ""
		}
		title := one("title")
		if title == "" {
			continue
		}
		issue := Issue{Title: title, Description: one("description"), DueDate: one("due"), URL: one("url")}
		issue.CreatedAt, issue.UpdatedAt = csvTime(one("created")), csvTime(one("updated"))
		for field, target := range map[string]**time.Time{"completed": &issue.CompletedAt, "started": &issue.StartedAt} {
			// A cancelled date fills the same slot as a completed one.
			for _, value := range all(field) {
				if at := csvTime(value); !at.IsZero() && *target == nil {
					*target = &at
				}
			}
		}
		issue.Archived = one("archived") != "" && !strings.EqualFold(one("archived"), "false")
		if due := csvTime(issue.DueDate); !due.IsZero() {
			issue.DueDate = due.Format(time.DateOnly)
		} else {
			issue.DueDate = ""
		}

		// The team: the one the row names, else the one its key belongs to,
		// else the default.
		key, teamTitle := one("teamkey"), one("teamname")
		id := one("id")
		if m := csvIssueKey.FindStringSubmatch(id); m != nil {
			number, _ := strconv.Atoi(m[2])
			issue.Number = int32(number)
			if key == "" {
				key = m[1]
			}
		}
		if key == "" && teamTitle == "" {
			teamTitle = teamName
		}
		if teamTitle == "" {
			teamTitle = key
		}
		teamID := teamKey(key, teamTitle)
		info := teams[teamID]
		if info == nil {
			info = &teamInfo{team: &Team{ID: teamID, Key: teamID, Name: teamTitle}, states: map[string]*csvStatus{}}
			teams[teamID] = info
			teamOrder = append(teamOrder, teamID)
		}
		issue.Team = teamID
		if id == "" {
			// No ID to know it by next time: its title and creation date will do.
			sum := sha256.Sum256([]byte(title + "\x00" + one("created")))
			id = "row:" + hex.EncodeToString(sum[:8])
		}
		issue.ID = teamID + "/" + id
		if parent := one("parent"); parent != "" {
			issue.Parent = teamID + "/" + parent
			// A parent in another team is named by that team's key.
			if m := csvIssueKey.FindStringSubmatch(parent); m != nil {
				issue.Parent = teamKey(m[1], m[1]) + "/" + parent
			}
		}

		if status := one("status"); status != "" {
			stateID := teamID + ":" + strings.ToLower(status)
			seen := info.states[stateID]
			if seen == nil {
				seen = &csvStatus{name: status}
				info.states[stateID] = seen
				info.team.States = append(info.team.States, State{ID: stateID, Name: status})
			}
			seen.rows++
			if issue.CompletedAt != nil {
				seen.closed++
			}
			issue.State = stateID
		}
		issue.Priority = csvPriority(one("priority"))
		person := func(value string) string {
			if value == "" {
				return ""
			}
			id := strings.ToLower(value)
			if !users[id] {
				users[id] = true
				u := User{ID: id, Name: value}
				if strings.Contains(value, "@") {
					u.Email = value
				}
				file.Users = append(file.Users, u)
			}
			return id
		}
		issue.Assignee, issue.Creator = person(one("assignee")), person(one("creator"))
		for _, cell := range all("labels") {
			for _, label := range strings.FieldsFunc(cell, func(r rune) bool { return r == ',' || r == ';' }) {
				if label = strings.TrimSpace(label); label != "" {
					if !labels[strings.ToLower(label)] {
						labels[strings.ToLower(label)] = true
						file.Labels = append(file.Labels, Label{ID: label, Name: label})
					}
					issue.Labels = append(issue.Labels, label)
				}
			}
		}
		if project := one("project"); project != "" {
			id := "project:" + strings.ToLower(project)
			if !projects[id] {
				projects[id] = true
				file.Projects = append(file.Projects, Project{ID: id, Name: project})
			}
			issue.Project = id
		}
		file.Issues = append(file.Issues, issue)
	}
	if len(file.Issues) == 0 {
		return nil, userError("None of the rows has a title.")
	}
	for _, id := range teamOrder {
		info := teams[id]
		for i, state := range info.team.States {
			info.team.States[i].Kind = info.states[state.ID].kind()
		}
		file.Teams = append(file.Teams, *info.team)
	}
	return &csvSource{file: file}, nil
}

// csvStatus is what the file shows about a status, from which its kind is
// worked out: a CSV names statuses but doesn't say what they mean.
type csvStatus struct {
	name   string
	rows   int
	closed int
}

func (s *csvStatus) kind() string {
	switch {
	case canceledWords.MatchString(s.name):
		return KindCanceled
	// Most issues in it have a completion date, or it sounds finished.
	case s.closed*2 > s.rows || csvCompleted.MatchString(s.name):
		return KindCompleted
	case csvStarted.MatchString(s.name):
		return KindStarted
	case csvBacklog.MatchString(s.name):
		return KindBacklog
	case strings.Contains(strings.ToLower(s.name), "triage"):
		return KindTriage
	}
	return KindUnstarted
}

func csvPriority(raw string) int {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "urgent", "highest", "critical", "blocker":
		return 1
	case "2", "high", "major":
		return 2
	case "3", "medium", "normal":
		return 3
	case "4", "low", "lowest", "minor", "trivial":
		return 4
	}
	return 0
}

func (s *csvSource) inner() Source {
	return &fileSource{file: s.file, teams: s.teams}
}

// OnlyTeams limits a CSV source to the given teams (by the IDs its preview
// gives them); none means all.
func OnlyTeams(source Source, teams []string) Source {
	if s, ok := source.(*csvSource); ok {
		s.teams = teams
	}
	return source
}

func (s *csvSource) Preview(ctx context.Context) (Preview, error) { return s.inner().Preview(ctx) }

func (s *csvSource) Fetch(ctx context.Context, emit func(Bundle) error, progress func(string)) error {
	return s.inner().Fetch(ctx, emit, progress)
}

func (s *csvSource) Open(ctx context.Context, a Attachment) (ReadCloser, string, error) {
	return s.inner().Open(ctx, a)
}
