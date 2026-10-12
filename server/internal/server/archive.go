package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	archiveEvery = time.Hour
	// archiveBatch is how many issues are archived per transaction, which
	// bounds how long a workspace's sync log is locked.
	archiveBatch = 100
)

// LookupIssue finds an issue by ID or by its KEY-number, archived or not.
func (s *Server) LookupIssue(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId, params api.LookupIssueParams) {
	if _, ok := s.requireWorkspaceRole(w, r, workspaceId); !ok {
		return
	}
	ctx := r.Context()
	var (
		issue dbgen.Issue
		err   error
	)
	if id, parseErr := uuid.Parse(params.Ref); parseErr == nil {
		issue, err = s.q.GetIssue(ctx, id)
	} else if key, number, ok := parseIssueRef(params.Ref); ok {
		issue, err = s.q.GetIssueByTeamKeyAndNumber(ctx, dbgen.GetIssueByTeamKeyAndNumberParams{WorkspaceID: workspaceId, Key: key, Number: number})
	} else {
		notFound(w)
		return
	}
	if db.IsNoRows(err) || (err == nil && issue.WorkspaceID != workspaceId) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// The same rule as everywhere else: the caller must be able to see the team.
	if _, ok := s.requireTeamAccess(w, r, issue.TeamID); !ok {
		return
	}
	writeJSON(w, http.StatusOK, issueRecord(issue))
}

// parseIssueRef splits "ENG-12" into its team key and number.
func parseIssueRef(ref string) (key string, number int32, ok bool) {
	key, digits, found := strings.Cut(strings.TrimSpace(ref), "-")
	n, err := strconv.ParseInt(digits, 10, 32)
	if !found || err != nil || n <= 0 || key == "" {
		return "", 0, false
	}
	return strings.ToUpper(key), int32(n), true
}

func (s *Server) UnarchiveIssue(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	ctx := r.Context()
	actorID := currentSession(r).User.ID

	var result api.IssueMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		before, err := q.GetIssueForUpdate(ctx, issueId)
		if err != nil {
			return err
		}
		if before.ArchivedAt == nil {
			return currentIssue(ctx, q, team, issueId, &result)
		}
		issue, err := q.UnarchiveIssue(ctx, issueId)
		if err != nil {
			return err
		}
		result.Issue = issueRecord(issue)
		// An update without archivedAt puts the issue back in every client's copy.
		if result.SyncId, err = recordIssue(ctx, q, team, issue, syncer.Update); err != nil {
			return err
		}
		return logActivities(ctx, q, team, issue, &actorID, []activity{{kind: api.ActivityTypeUnarchived}})
	})
	s.respond(w, r, err, result)
}

// archivedPage is how many archived issues are listed at a time.
const archivedPage = 50

func (s *Server) ListArchivedIssues(w http.ResponseWriter, r *http.Request, teamId api.TeamId, params api.ListArchivedIssuesParams) {
	if _, ok := s.requireTeamAccess(w, r, teamId); !ok {
		return
	}
	before := time.Now().Add(time.Hour) // everything
	if params.Before != nil {
		before = *params.Before
	}
	// One more than a page, to know whether there is another.
	rows, err := s.q.ListArchivedIssues(r.Context(), dbgen.ListArchivedIssuesParams{TeamID: teamId, Before: before, Limit: archivedPage + 1})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := api.ArchivedIssues{Issues: make([]api.Issue, 0, len(rows)), More: len(rows) > archivedPage}
	for _, row := range rows[:min(len(rows), archivedPage)] {
		out.Issues = append(out.Issues, issueRecord(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// ArchiveClosedIssues archives issues that have been closed for longer than
// their team's auto-archive window, now and then hourly until ctx is done.
// Archived issues leave every client's local copy; they stay searchable and
// can be opened and unarchived.
func (s *Server) ArchiveClosedIssues(ctx context.Context) {
	ticker := time.NewTicker(archiveEvery)
	defer ticker.Stop()
	for {
		// Canceling first means what it cancels is archived in its own time.
		if _, err := s.closeUntouchedIssues(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.log.Warn("auto-close", "err", err)
		}
		if _, err := s.archiveClosedIssues(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.log.Warn("auto-archive", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// archiveClosedIssues runs one pass and returns how many issues it archived.
func (s *Server) archiveClosedIssues(ctx context.Context, now time.Time) (int, error) {
	teams, err := s.q.ListTeamsWithAutoArchive(ctx)
	if err != nil {
		return 0, err
	}
	archived := 0
	for _, team := range teams {
		before := now.AddDate(0, 0, -int(team.AutoArchiveDays))
		for {
			var n int
			err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
				due, err := q.ListIssuesToArchive(ctx, dbgen.ListIssuesToArchiveParams{TeamID: team.ID, Before: before, Limit: archiveBatch})
				if err != nil {
					return err
				}
				for _, issue := range due {
					updated, err := q.ArchiveIssue(ctx, issue.ID)
					if err != nil {
						return err
					}
					if _, err := recordIssue(ctx, q, team, updated, syncer.Update); err != nil {
						return err
					}
					// No actor: the system put it away.
					if err := logActivities(ctx, q, team, updated, nil, []activity{{kind: api.ActivityTypeArchived}}); err != nil {
						return err
					}
				}
				n = len(due)
				return nil
			})
			if err != nil {
				return archived, err
			}
			archived += n
			if n < archiveBatch {
				break
			}
		}
	}
	return archived, nil
}

// closeUntouchedIssues cancels, for each team that asked, the issues that
// were never started and that nobody has touched within the team's window.
// It goes through the same path as a person canceling them, so followers
// are told and can reopen what they still want. It returns how many it closed.
func (s *Server) closeUntouchedIssues(ctx context.Context, now time.Time) (int, error) {
	teams, err := s.q.ListTeamsWithAutoClose(ctx)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, team := range teams {
		canceled, ok, err := firstStateOfType(ctx, s.q, team.ID, api.WorkflowStateTypeCanceled)
		if err != nil {
			return closed, err
		}
		if !ok {
			continue // the team has no canceled status to move them to
		}
		before := now.AddDate(0, 0, -int(team.AutoCloseDays))
		for {
			var n int
			err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
				due, err := q.ListIssuesToAutoClose(ctx, dbgen.ListIssuesToAutoCloseParams{TeamID: team.ID, Before: before, Limit: archiveBatch})
				if err != nil {
					return err
				}
				for _, issue := range due {
					// No actor: the system closed it.
					update := issueUpdate{params: dbgen.UpdateIssueParams{ID: issue.ID, StateID: &canceled.ID}, changes: true}
					if _, err := applyIssueUpdate(ctx, q, team, nil, update); err != nil && !errors.Is(err, errNoChange) {
						return err
					}
				}
				n = len(due)
				return nil
			})
			if err != nil {
				return closed, err
			}
			closed += n
			if n < archiveBatch {
				break
			}
		}
	}
	return closed, nil
}
