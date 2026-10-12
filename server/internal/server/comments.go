package server

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const maxCommentLen = 50_000 // bytes

// The reactions the app offers; anything else is refused.
var allowedReactions = []string{"👍", "👎", "❤️", "🎉", "😄", "👀", "🚀"}

func (s *Server) CreateComment(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	var req api.CreateCommentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Comment ID is required.")
		return
	}
	if apiErr := validateCommentBody(req.Body); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()
	authorID := currentSession(r).User.ID

	// existing answers a retry of a create that already went through.
	existing := func(q *dbgen.Queries) (*api.CommentMutationResult, error) {
		comment, err := q.GetComment(ctx, req.Id)
		if db.IsNoRows(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if comment.IssueID != issueId {
			return nil, &apiError{http.StatusConflict, "id_taken", "That comment ID is already in use."}
		}
		syncID, err := q.GetLastSyncID(ctx, team.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return &api.CommentMutationResult{Comment: commentRecord(comment), SyncId: syncID}, nil
	}

	var (
		result  api.CommentMutationResult
		created bool
	)
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if found, err := existing(q); err != nil {
			return err
		} else if found != nil {
			result = *found
			return nil
		}
		issue, err := q.GetIssueForUpdate(ctx, issueId)
		if err != nil {
			return err
		}
		comment, err := q.CreateComment(ctx, dbgen.CreateCommentParams{
			ID:          req.Id,
			WorkspaceID: team.WorkspaceID,
			IssueID:     issueId,
			UserID:      &authorID,
			Body:        req.Body,
		})
		if err != nil {
			return err
		}
		result.Comment = commentRecord(comment)
		if result.SyncId, err = recordComment(ctx, q, team, comment, syncer.Insert); err != nil {
			return err
		}
		created = true

		// Commenting, or being mentioned, makes you a follower of the issue.
		mentions, err := visibleTo(ctx, q, team, mentionedUsers(req.Body))
		if err != nil {
			return err
		}
		followers := issue.SubscriberIds
		subscribers := uniqueIDs(append(append(slices.Clone(followers), authorID), mentions...))
		if len(subscribers) != len(followers) {
			updated, err := q.SetIssueSubscribers(ctx, dbgen.SetIssueSubscribersParams{ID: issueId, SubscriberIds: subscribers})
			if err != nil {
				return err
			}
			if _, err := recordIssue(ctx, q, team, updated, syncer.Update); err != nil {
				return err
			}
		}

		// Mentioned people hear about the mention; other followers about the comment.
		if err := notify(ctx, q, team, issue, authorID, api.NotificationTypeMentioned, &comment.ID, mentions); err != nil {
			return err
		}
		return notify(ctx, q, team, issue, authorID, api.NotificationTypeCommented, &comment.ID, without(followers, mentions))
	})
	if db.IsUniqueViolation(err) {
		// A concurrent retry created the same comment first.
		var found *api.CommentMutationResult
		if found, err = existing(s.q); err == nil && found != nil {
			result = *found
		} else if err == nil {
			err = &apiError{http.StatusConflict, "conflict", "The comment could not be created. Try again."}
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if created {
		writeJSON(w, http.StatusCreated, result)
	} else {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) UpdateComment(w http.ResponseWriter, r *http.Request, commentId api.CommentId) {
	comment, access, ok := s.requireComment(w, r, commentId)
	if !ok {
		return
	}
	if comment.UserID == nil || *comment.UserID != currentSession(r).User.ID {
		forbidden(w)
		return
	}
	var req api.UpdateCommentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if apiErr := validateCommentBody(req.Body); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	result := api.CommentMutationResult{Comment: commentRecord(comment)}
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if req.Body == comment.Body {
			var err error
			result.SyncId, err = q.GetLastSyncID(ctx, comment.WorkspaceID)
			return err
		}
		updated, err := q.UpdateCommentBody(ctx, dbgen.UpdateCommentBodyParams{ID: commentId, Body: req.Body})
		if err != nil {
			return err
		}
		result.Comment = commentRecord(updated)
		result.SyncId, err = recordComment(ctx, q, access.team, updated, syncer.Update)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) DeleteComment(w http.ResponseWriter, r *http.Request, commentId api.CommentId) {
	comment, access, ok := s.requireComment(w, r, commentId)
	if !ok {
		return
	}
	isAuthor := comment.UserID != nil && *comment.UserID == currentSession(r).User.ID
	if !isAuthor && access.role != roleAdmin {
		forbidden(w)
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteComment(ctx, commentId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = recordComment(ctx, q, access.team, comment, syncer.Delete)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ack)
}

func (s *Server) AddReaction(w http.ResponseWriter, r *http.Request, commentId api.CommentId, emoji string) {
	s.setReaction(w, r, commentId, emoji, true)
}

func (s *Server) RemoveReaction(w http.ResponseWriter, r *http.Request, commentId api.CommentId, emoji string) {
	s.setReaction(w, r, commentId, emoji, false)
}

func (s *Server) setReaction(w http.ResponseWriter, r *http.Request, commentID uuid.UUID, emoji string, on bool) {
	comment, access, ok := s.requireComment(w, r, commentID)
	if !ok {
		return
	}
	if !slices.Contains(allowedReactions, emoji) {
		writeError(w, http.StatusBadRequest, "invalid_reaction", "That reaction isn't available.")
		return
	}
	ctx := r.Context()
	userID := currentSession(r).User.ID

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var (
			changed int64
			err     error
			action  = syncer.Insert
		)
		if on {
			changed, err = q.AddReaction(ctx, dbgen.AddReactionParams{CommentID: commentID, UserID: userID, Emoji: emoji})
		} else {
			action = syncer.Delete
			changed, err = q.RemoveReaction(ctx, dbgen.RemoveReactionParams{CommentID: commentID, UserID: userID, Emoji: emoji})
		}
		if err != nil {
			return err
		}
		if changed == 0 {
			ack.SyncId, err = q.GetLastSyncID(ctx, comment.WorkspaceID)
			return err
		}
		ack.SyncId, err = syncer.Record(ctx, q, syncer.Change{
			WorkspaceID: comment.WorkspaceID,
			Model:       api.SyncModelReaction,
			ModelID:     commentID,
			Action:      action,
			Data:        api.Reaction{CommentId: commentID, UserId: userID, Emoji: emoji},
			Team:        &access.team,
		})
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ack)
}

// requireComment loads a comment on an issue the caller is allowed to see.
func (s *Server) requireComment(w http.ResponseWriter, r *http.Request, commentID uuid.UUID) (dbgen.Comment, teamAccess, bool) {
	comment, err := s.q.GetComment(r.Context(), commentID)
	if db.IsNoRows(err) {
		notFound(w)
		return comment, teamAccess{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return comment, teamAccess{}, false
	}
	issue, err := s.q.GetIssue(r.Context(), comment.IssueID)
	if err != nil {
		s.internalError(w, r, err)
		return comment, teamAccess{}, false
	}
	access, ok := s.requireTeamAccess(w, r, issue.TeamID)
	return comment, access, ok
}

func recordComment(ctx context.Context, q *dbgen.Queries, team dbgen.Team, comment dbgen.Comment, action string) (int64, error) {
	return syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: comment.WorkspaceID,
		Model:       api.SyncModelComment,
		ModelID:     comment.ID,
		Action:      action,
		Data:        commentRecord(comment),
		Team:        &team,
	})
}

func validateCommentBody(body string) *apiError {
	if strings.TrimSpace(body) == "" {
		return badRequest("invalid_body", "A comment can't be empty.")
	}
	if len(body) > maxCommentLen {
		return badRequest("invalid_body", "That comment is too long.")
	}
	return nil
}
