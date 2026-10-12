package server

import (
	"net/http"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

func (s *Server) UpdateNotification(w http.ResponseWriter, r *http.Request, notificationId uuid.UUID) {
	n, ok := s.requireNotification(w, r, notificationId)
	if !ok {
		return
	}
	var req api.UpdateNotificationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if (n.ReadAt != nil) == req.Read {
			var err error
			ack.SyncId, err = q.GetLastSyncID(ctx, n.WorkspaceID)
			return err
		}
		updated, err := q.SetNotificationRead(ctx, dbgen.SetNotificationReadParams{ID: n.ID, Read: req.Read})
		if err != nil {
			return err
		}
		ack.SyncId, err = recordNotification(ctx, q, updated, syncer.Update)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ack)
}

func (s *Server) DeleteNotification(w http.ResponseWriter, r *http.Request, notificationId uuid.UUID) {
	n, ok := s.requireNotification(w, r, notificationId)
	if !ok {
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteNotification(ctx, n.ID); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = recordNotification(ctx, q, n, syncer.Delete)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ack)
}

// requireNotification loads one of the caller's own notifications. Anyone
// else's is reported as missing.
func (s *Server) requireNotification(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Notification, bool) {
	n, err := s.q.GetNotification(r.Context(), id)
	if db.IsNoRows(err) || (err == nil && (n.UserID != currentSession(r).User.ID || !currentSession(r).allows(n.WorkspaceID))) {
		notFound(w)
		return n, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return n, false
	}
	return n, true
}
