package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	maxImportBytes = 5 << 20
	maxImportRows  = 2000
)

// importColumns maps the header names a CSV may use (lowercased, letters and
// digits only) to the issue field they fill. The names cover what Linear,
// Jira, GitHub and plain spreadsheets call these things.
var importColumns = map[string]string{
	"title": "title", "name": "title", "summary": "title", "subject": "title",
	"description": "description", "body": "description", "details": "description",
	"status": "status", "state": "status",
	"priority": "priority",
	"assignee": "assignee", "assigneeemail": "assignee", "assignedto": "assignee", "owner": "assignee",
	"labels": "labels", "label": "labels", "tags": "labels",
	"duedate": "due", "due": "due",
}

// importRow is one issue to create, as written in the file.
type importRow struct {
	line                                                int32
	title, description, status, priority, assignee, due string
	labels                                              []string
}

// importLabelColors are given to labels the import creates, in turn.
var importLabelColors = []string{"#5e6ad2", "#26b5ce", "#4cb782", "#f2c94c", "#f2994a", "#eb5757", "#bb87fc", "#95a2b3"}

func (s *Server) ImportIssues(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	access, ok := s.requireTeamAccess(w, r, teamId)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "The file is too large. The limit is 5 MB.")
		return
	}
	rows, skipped, apiErr := parseImport(body)
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()
	team := access.team
	creatorID := currentSession(r).User.ID
	result := api.ImportResult{Skipped: skipped}

	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if len(rows) == 0 {
			return nil
		}
		lookup, err := loadImportLookup(ctx, q, team, access)
		if err != nil {
			return err
		}
		first, err := q.ReserveIssueNumbers(ctx, dbgen.ReserveIssueNumbersParams{ID: team.ID, Count: int32(len(rows))})
		if err != nil {
			return err
		}
		top, err := q.MinIssueSortOrder(ctx, team.ID)
		if err != nil {
			return err
		}
		for i, row := range rows {
			labelIDs := []uuid.UUID{}
			for _, name := range row.labels {
				id, created, err := lookup.label(ctx, q, name)
				if err != nil {
					return err
				}
				if created {
					result.LabelsCreated++
				}
				if id != uuid.Nil && !slices.Contains(labelIDs, id) && len(labelIDs) < maxIssueLabels {
					labelIDs = append(labelIDs, id)
				}
			}
			assigneeID := lookup.assignee(row.assignee)
			subscribers := []uuid.UUID{creatorID}
			if assigneeID != nil {
				subscribers = append(subscribers, *assigneeID)
			}
			issue, err := q.CreateIssue(ctx, dbgen.CreateIssueParams{
				ID:          newID(),
				WorkspaceID: team.WorkspaceID,
				TeamID:      team.ID,
				Number:      first + int32(i),
				Title:       row.title,
				Description: row.description,
				StateID:     lookup.state(row.status),
				Priority:    importPriority(row.priority),
				AssigneeID:  assigneeID,
				CreatorID:   &creatorID,
				// The file's order, above everything already in the team.
				SortOrder:     top - float64(len(rows)-i),
				LabelIds:      labelIDs,
				DueDate:       importDate(row.due),
				SubscriberIds: uniqueIDs(subscribers),
			})
			if err != nil {
				return err
			}
			if _, err := recordIssue(ctx, q, team, issue, syncer.Insert); err != nil {
				return err
			}
			// No notifications: an import shouldn't flood anyone's inbox.
			if err := logActivities(ctx, q, team, issue, &creatorID, []activity{{kind: api.ActivityTypeCreated}}); err != nil {
				return err
			}
			result.Created++
		}
		return nil
	})
	s.respond(w, r, err, result)
}

// parseImport reads a CSV file into the issues to create, plus the rows
// that can't become issues and why.
func parseImport(body []byte) ([]importRow, []api.ImportSkippedRow, *apiError) {
	skipped := []api.ImportSkippedRow{}
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")) // byte order mark
	if !utf8.Valid(body) {
		return nil, nil, badRequest("invalid_csv", "The file isn't UTF-8 text. Export it as CSV (UTF-8) and try again.")
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.Comma = importDelimiter(body)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, nil, badRequest("invalid_csv", "The file is empty or isn't a CSV file.")
	}
	columns := make(map[string]int)
	for i, name := range header {
		if field, ok := importColumns[importHeaderKey(name)]; ok {
			if _, taken := columns[field]; !taken {
				columns[field] = i
			}
		}
	}
	if _, ok := columns["title"]; !ok {
		return nil, nil, badRequest("invalid_csv", "The file needs a Title column.")
	}

	var rows []importRow
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		var parseErr *csv.ParseError
		if errors.As(err, &parseErr) {
			skipped = append(skipped, api.ImportSkippedRow{Row: int32(parseErr.StartLine), Reason: "The row couldn't be read."})
			continue
		}
		if err != nil {
			return nil, nil, badRequest("invalid_csv", "The file couldn't be read as CSV.")
		}
		line, _ := reader.FieldPos(0)
		cell := func(field string) string {
			if i, ok := columns[field]; ok && i < len(record) {
				return strings.TrimSpace(record[i])
			}
			return ""
		}
		blank := true
		for _, value := range record {
			if strings.TrimSpace(value) != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		row := importRow{
			line:        int32(line),
			title:       cell("title"),
			description: cell("description"),
			status:      cell("status"),
			priority:    cell("priority"),
			assignee:    cell("assignee"),
			due:         cell("due"),
		}
		for _, name := range strings.FieldsFunc(cell("labels"), func(c rune) bool { return c == ',' || c == ';' || c == '|' }) {
			if name = strings.TrimSpace(name); name != "" {
				row.labels = append(row.labels, name)
			}
		}
		switch {
		case row.title == "":
			skipped = append(skipped, api.ImportSkippedRow{Row: row.line, Reason: "The row has no title."})
		case utf8.RuneCountInString(row.title) > maxIssueTitleLen:
			skipped = append(skipped, api.ImportSkippedRow{Row: row.line, Reason: "The title is longer than 500 characters."})
		case len(row.description) > maxIssueDescriptionLen:
			skipped = append(skipped, api.ImportSkippedRow{Row: row.line, Reason: "The description is too long."})
		default:
			rows = append(rows, row)
		}
		if len(rows) > maxImportRows {
			return nil, nil, badRequest("too_many_rows", "The file has more than 2,000 issues. Split it and import the parts.")
		}
	}
	return rows, skipped, nil
}

// importDelimiter guesses the separator from the header line: spreadsheets
// in some locales export with semicolons, and some tools with tabs.
func importDelimiter(body []byte) rune {
	header, _, _ := bytes.Cut(body, []byte("\n"))
	best, bestCount := ',', bytes.Count(header, []byte(","))
	for _, candidate := range []rune{';', '\t'} {
		if n := bytes.Count(header, []byte(string(candidate))); n > bestCount {
			best, bestCount = candidate, n
		}
	}
	return best
}

// importHeaderKey reduces a header to lowercase letters and digits, so that
// "Due Date", "due_date" and "dueDate" are the same column.
func importHeaderKey(name string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(name) {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// importLookup resolves the names in an import file to records.
type importLookup struct {
	team         dbgen.Team
	defaultState uuid.UUID
	// forceState is set when every issue must land in triage.
	forceState   bool
	stateByName  map[string]uuid.UUID
	stateByType  map[string]uuid.UUID
	members      map[string]uuid.UUID // by lowercased email and name
	labels       map[string]uuid.UUID // by lowercased name
	createLabels bool
	created      int
}

func loadImportLookup(ctx context.Context, q *dbgen.Queries, team dbgen.Team, access teamAccess) (*importLookup, error) {
	l := &importLookup{
		team:        team,
		stateByName: map[string]uuid.UUID{},
		stateByType: map[string]uuid.UUID{},
		members:     map[string]uuid.UUID{},
		labels:      map[string]uuid.UUID{},
		// The same rule as creating a label by hand.
		createLabels: access.role != roleGuest,
	}
	state, err := q.GetDefaultWorkflowState(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	l.defaultState = state.ID
	if team.TriageEnabled && !access.isMember {
		// Issues from outside the team wait in triage, as when created by hand.
		if triage, err := q.GetTriageState(ctx, team.ID); err == nil {
			l.defaultState, l.forceState = triage.ID, true
		} else if !db.IsNoRows(err) {
			return nil, err
		}
	}
	states, err := q.ListWorkflowStatesForTeams(ctx, []uuid.UUID{team.ID})
	if err != nil {
		return nil, err
	}
	for _, st := range states { // in position order: the first of a type wins
		if st.Type == string(api.WorkflowStateTypeTriage) {
			continue
		}
		l.stateByName[strings.ToLower(st.Name)] = st.ID
		if _, ok := l.stateByType[st.Type]; !ok {
			l.stateByType[st.Type] = st.ID
		}
	}
	members, err := q.ListWorkspaceMembers(ctx, team.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		l.members[strings.ToLower(m.User.Email)] = m.User.ID
		if m.User.Name != "" {
			l.members[strings.ToLower(m.User.Name)] = m.User.ID
		}
	}
	labels, err := q.ListLabels(ctx, team.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for _, label := range labels {
		l.labels[strings.ToLower(label.Name)] = label.ID
	}
	return l, nil
}

// importStatusTypes maps the words other tools use for a status to the type
// of workflow state they mean.
var importStatusTypes = map[string]string{
	"backlog": "backlog", "icebox": "backlog",
	"todo": "unstarted", "to do": "unstarted", "open": "unstarted", "new": "unstarted", "unstarted": "unstarted", "planned": "unstarted",
	"in progress": "started", "started": "started", "doing": "started", "in review": "started",
	"done": "completed", "completed": "completed", "closed": "completed", "resolved": "completed", "fixed": "completed",
	"canceled": "canceled", "cancelled": "canceled", "wontfix": "canceled", "won't do": "canceled", "wont do": "canceled", "duplicate": "canceled",
}

// state picks the workflow state for a status as written in the file: one
// of the team's states by name, else one of the right kind, else the default.
func (l *importLookup) state(status string) uuid.UUID {
	status = strings.ToLower(status)
	if l.forceState || status == "" {
		return l.defaultState
	}
	if id, ok := l.stateByName[status]; ok {
		return id
	}
	if id, ok := l.stateByType[importStatusTypes[status]]; ok {
		return id
	}
	return l.defaultState
}

// assignee finds a workspace member by email or name; nil if there is none.
func (l *importLookup) assignee(who string) *uuid.UUID {
	if id, ok := l.members[strings.ToLower(who)]; ok && who != "" {
		return &id
	}
	return nil
}

// label returns the label with the given name, creating it if the importer
// may. A zero ID means the label doesn't exist and wasn't created.
func (l *importLookup) label(ctx context.Context, q *dbgen.Queries, name string) (id uuid.UUID, created bool, err error) {
	if id, ok := l.labels[strings.ToLower(name)]; ok {
		return id, false, nil
	}
	name, apiErr := cleanLabelName(name)
	if apiErr != nil || !l.createLabels {
		return uuid.Nil, false, nil
	}
	label, err := q.CreateLabel(ctx, dbgen.CreateLabelParams{
		ID:          newID(),
		WorkspaceID: l.team.WorkspaceID,
		Name:        name,
		Color:       importLabelColors[l.created%len(importLabelColors)],
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	if _, err := recordLabel(ctx, q, label, syncer.Insert); err != nil {
		return uuid.Nil, false, err
	}
	l.created++
	l.labels[strings.ToLower(name)] = label.ID
	return label.ID, true, nil
}

// importPriority reads a priority as a number (0-4) or a word.
func importPriority(raw string) int16 {
	switch strings.ToLower(raw) {
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

var importDateLayouts = []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05", "2006/01/02", "01/02/2006", "1/2/2006", "Jan 2, 2006", "2 Jan 2006"}

// importDate reads a date in the formats exports commonly use.
func importDate(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	for _, layout := range importDateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			return &day
		}
	}
	return nil
}
