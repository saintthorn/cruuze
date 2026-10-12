package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	// maxEntrySeconds caps one entry at a week: longer is a forgotten timer.
	maxEntrySeconds = 7 * 24 * 60 * 60
	maxTimeNoteLen  = 500
	// maxExportDays bounds a timesheet export.
	maxExportDays = 366
)

func timeEntryRecord(t dbgen.TimeEntry) api.TimeEntry {
	return api.TimeEntry{
		Id:        t.ID,
		IssueId:   t.IssueID,
		UserId:    t.UserID,
		StartedAt: t.StartedAt,
		EndedAt:   t.EndedAt,
		Seconds:   t.Seconds,
		Note:      t.Note,
	}
}

// recordTimeEntry logs a change to an entry for its owner alone.
func recordTimeEntry(ctx context.Context, q *dbgen.Queries, entry dbgen.TimeEntry, action string) (int64, error) {
	return record(ctx, q, entry.WorkspaceID, api.SyncModelTimeEntry, entry.ID, action, timeEntryRecord(entry), nil, &entry.UserID)
}

// refreshIssueTime recomputes an issue's logged total and tells everyone who
// can see the issue. It returns the sync ID of that change.
func refreshIssueTime(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issueID uuid.UUID) (int64, error) {
	issue, err := q.SumIssueTime(ctx, issueID)
	if err != nil {
		return 0, err
	}
	return recordIssue(ctx, q, team, issue, syncer.Update)
}

// stopEntry ends a running entry at the given time.
func stopEntry(ctx context.Context, q *dbgen.Queries, entry dbgen.TimeEntry, at time.Time) (dbgen.TimeEntry, error) {
	if at.Before(entry.StartedAt) {
		at = entry.StartedAt
	}
	seconds := int32(min(int64(at.Sub(entry.StartedAt)/time.Second), maxEntrySeconds))
	return q.UpdateTimeEntry(ctx, dbgen.UpdateTimeEntryParams{ID: entry.ID, EndedAt: &at, Seconds: &seconds})
}

func (s *Server) CreateTimeEntry(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	var req api.CreateTimeEntryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	now := time.Now()
	params := dbgen.CreateTimeEntryParams{
		ID:          req.Id,
		WorkspaceID: team.WorkspaceID,
		IssueID:     issueId,
		UserID:      currentSession(r).User.ID,
		StartedAt:   req.StartedAt,
		EndedAt:     req.EndedAt,
	}
	if req.Note != nil {
		params.Note = truncate(*req.Note, maxTimeNoteLen)
	}
	var apiErr *apiError
	switch {
	case req.Id == uuid.Nil || req.StartedAt.IsZero() || req.StartedAt.After(now.Add(time.Minute)):
		apiErr = badRequest("invalid_time_entry", "A time entry needs an ID and a start that isn't in the future.")
	case req.EndedAt == nil && req.Seconds != nil:
		apiErr = badRequest("invalid_time_entry", "A running timer has no duration yet.")
	case req.EndedAt != nil:
		// A finished stretch: at most as long as the stretch, and not absurd.
		span := int64(req.EndedAt.Sub(req.StartedAt) / time.Second)
		if req.Seconds == nil || *req.Seconds <= 0 || int64(*req.Seconds) > span+60 || *req.Seconds > maxEntrySeconds || req.EndedAt.After(now.Add(time.Minute)) {
			apiErr = badRequest("invalid_time_entry", "The time logged must be more than nothing, fit between the start and the end, and be at most a week.")
		} else {
			params.Seconds = *req.Seconds
		}
	}
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if existing, err := q.GetTimeEntry(ctx, req.Id); err == nil {
			// A retry of a create that already went through.
			if existing.UserID != params.UserID || existing.IssueID != issueId {
				return errIDTaken
			}
			created = false
			ack.SyncId, err = q.GetLastSyncID(ctx, team.WorkspaceID)
			return err
		} else if !db.IsNoRows(err) {
			return err
		}
		if params.EndedAt == nil {
			// One timer at a time: starting this one stops the other.
			if err := stopRunningTimer(ctx, q, params.UserID, now, currentSession(r).keyWorkspace); err != nil {
				return err
			}
		}
		entry, err := q.CreateTimeEntry(ctx, params)
		if err != nil {
			return err
		}
		if ack.SyncId, err = recordTimeEntry(ctx, q, entry, syncer.Insert); err != nil {
			return err
		}
		if entry.EndedAt != nil {
			ack.SyncId, err = refreshIssueTime(ctx, q, team, issueId)
		}
		return err
	})
	s.respondCreated(w, r, err, created, ack)
}

// stopRunningTimer ends the user's running timer, if they have one. An API
// key (keyWorkspace) only ends a timer in its own workspace.
func stopRunningTimer(ctx context.Context, q *dbgen.Queries, userID uuid.UUID, at time.Time, keyWorkspace *uuid.UUID) error {
	running, err := q.GetRunningTimeEntry(ctx, userID)
	if db.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// The timer may be on an issue of another team, even another workspace.
	issue, err := q.GetIssue(ctx, running.IssueID)
	if err != nil {
		return err
	}
	team, err := q.GetTeam(ctx, issue.TeamID)
	if err != nil {
		return err
	}
	if keyWorkspace != nil && team.WorkspaceID != *keyWorkspace {
		return &apiError{http.StatusConflict, "timer_running", "A timer is running in another workspace. Stop it first."}
	}
	stopped, err := stopEntry(ctx, q, running, at)
	if err != nil {
		return err
	}
	if _, err := recordTimeEntry(ctx, q, stopped, syncer.Update); err != nil {
		return err
	}
	_, err = refreshIssueTime(ctx, q, team, stopped.IssueID)
	return err
}

func (s *Server) UpdateTimeEntry(w http.ResponseWriter, r *http.Request, timeEntryId uuid.UUID) {
	entry, team, ok := s.requireTimeEntry(w, r, timeEntryId)
	if !ok {
		return
	}
	var req api.UpdateTimeEntryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateTimeEntryParams{ID: entry.ID}
	if req.Note != nil {
		note := truncate(*req.Note, maxTimeNoteLen)
		params.Note = &note
	}
	ended := entry.EndedAt
	if req.EndedAt != nil {
		at := *req.EndedAt
		if at.Before(entry.StartedAt) || at.After(time.Now().Add(time.Minute)) {
			writeError(w, http.StatusBadRequest, "invalid_time_entry", "The end must be after the start and not in the future.")
			return
		}
		params.EndedAt, ended = &at, &at
	}
	if ended != nil {
		// A finished entry always has a duration: the one given, else the whole stretch.
		seconds := int32(min(int64(ended.Sub(entry.StartedAt)/time.Second), maxEntrySeconds))
		if req.Seconds != nil {
			seconds = *req.Seconds
		} else if entry.EndedAt != nil {
			seconds = entry.Seconds
		}
		if seconds <= 0 || seconds > maxEntrySeconds || int64(seconds) > int64(ended.Sub(entry.StartedAt)/time.Second)+60 {
			writeError(w, http.StatusBadRequest, "invalid_time_entry", "The time logged must be more than nothing, fit between the start and the end, and be at most a week.")
			return
		}
		params.Seconds = &seconds
	} else if req.Seconds != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_entry", "A running timer has no duration yet.")
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		updated, err := q.UpdateTimeEntry(ctx, params)
		if err != nil {
			return err
		}
		if ack.SyncId, err = recordTimeEntry(ctx, q, updated, syncer.Update); err != nil {
			return err
		}
		if updated.EndedAt != nil {
			ack.SyncId, err = refreshIssueTime(ctx, q, team, updated.IssueID)
		}
		return err
	})
	s.respond(w, r, err, ack)
}

func (s *Server) DeleteTimeEntry(w http.ResponseWriter, r *http.Request, timeEntryId uuid.UUID) {
	entry, team, ok := s.requireTimeEntry(w, r, timeEntryId)
	if !ok {
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteTimeEntry(ctx, entry.ID); err != nil {
			return err
		}
		var err error
		if ack.SyncId, err = recordTimeEntry(ctx, q, entry, syncer.Delete); err != nil {
			return err
		}
		if entry.EndedAt != nil {
			ack.SyncId, err = refreshIssueTime(ctx, q, team, entry.IssueID)
		}
		return err
	})
	s.respond(w, r, err, ack)
}

// requireTimeEntry loads one of the caller's own entries, with the team of
// its issue. Anyone else's is reported as missing.
func (s *Server) requireTimeEntry(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.TimeEntry, dbgen.Team, bool) {
	entry, err := s.q.GetTimeEntry(r.Context(), id)
	if db.IsNoRows(err) || (err == nil && entry.UserID != currentSession(r).User.ID) {
		notFound(w)
		return entry, dbgen.Team{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return entry, dbgen.Team{}, false
	}
	team, ok := s.requireIssueAccess(w, r, entry.IssueID)
	return entry, team, ok
}

func (s *Server) ExportTimeEntries(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId, params api.ExportTimeEntriesParams) {
	role, ok := s.requireWorkspaceRole(w, r, workspaceId)
	if !ok {
		return
	}
	since, until := params.From.Time, params.To.Time.AddDate(0, 0, 1)
	if !until.After(since) || until.Sub(since) > maxExportDays*24*time.Hour {
		writeError(w, http.StatusBadRequest, "invalid_range", "Choose a range of up to a year, ending on or after its first day.")
		return
	}
	ctx := r.Context()
	teamIDs, err := s.visibleTeamIDs(ctx, r, workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	query := dbgen.ExportTimeEntriesParams{WorkspaceID: workspaceId, Since: since, Until: until, TeamIds: teamIDs}
	if role != roleAdmin {
		// Everyone else gets their own time only.
		userID := currentSession(r).User.ID
		query.UserID = &userID
	}
	rows, err := s.q.ExportTimeEntries(ctx, query)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="time-%s-to-%s.csv"`, params.From.Format(time.DateOnly), params.To.Format(time.DateOnly)))
	out := csv.NewWriter(w)
	out.Write([]string{"Date", "Person", "Email", "Issue", "Title", "Hours", "Seconds", "Note"}) //nolint:errcheck // reported by Flush
	for _, row := range rows {
		out.Write([]string{ //nolint:errcheck // reported by Flush
			row.StartedAt.UTC().Format(time.DateOnly),
			csvSafe(row.Name),
			row.Email,
			fmt.Sprintf("%s-%d", row.TeamKey, row.Number),
			csvSafe(row.Title),
			strconv.FormatFloat(float64(row.Seconds)/3600, 'f', 2, 64),
			strconv.Itoa(int(row.Seconds)),
			csvSafe(row.Note),
		})
	}
	out.Flush()
	if err := out.Error(); err != nil {
		s.log.Warn("write time export", "err", err)
	}
}

// csvSafe keeps a cell from being run as a formula when the file is opened
// in a spreadsheet.
func csvSafe(value string) string {
	if value != "" && (value[0] == '=' || value[0] == '+' || value[0] == '-' || value[0] == '@' || value[0] == '\t' || value[0] == '\r') {
		return "'" + value
	}
	return value
}
