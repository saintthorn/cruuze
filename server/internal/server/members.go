package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

var (
	errLastAdmin = &apiError{http.StatusConflict, "last_admin", "A workspace needs at least one admin. Make someone else an admin first."}
	errNotMember = &apiError{http.StatusNotFound, "not_found", "That user is not a member of this workspace."}
)

func (s *Server) UpdateWorkspace(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	var req api.UpdateWorkspaceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_name", fmt.Sprintf("Workspace name must be 1-%d characters.", maxNameLen))
		return
	}
	ws, err := s.q.UpdateWorkspaceName(r.Context(), dbgen.UpdateWorkspaceNameParams{ID: workspaceId, Name: name})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWorkspace(ws, roleAdmin))
}

func (s *Server) UpdateWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId, userId uuid.UUID) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	var req api.UpdateMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	role := string(req.Role)
	if role != roleAdmin && role != roleMember && role != roleGuest {
		writeError(w, http.StatusBadRequest, "invalid_role", "Role must be admin, member or guest.")
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		member, err := lockedMember(ctx, q, workspaceId, userId)
		if err != nil {
			return err
		}
		if member.Role == role {
			ack.SyncId, err = q.GetLastSyncID(ctx, workspaceId)
			return err
		}
		if member.Role == roleAdmin {
			if err := requireAnotherAdmin(ctx, q, workspaceId); err != nil {
				return err
			}
		}
		if _, err := q.SetWorkspaceRole(ctx, dbgen.SetWorkspaceRoleParams{WorkspaceID: workspaceId, UserID: userId, Role: role}); err != nil {
			return err
		}
		// The member's own clients bootstrap again if this changes what they see.
		ack.SyncId, err = record(ctx, q, workspaceId, api.SyncModelUser, userId, syncer.Update, userRecord(member.User, role, member.JoinedAt), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

func (s *Server) RemoveWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId, userId uuid.UUID) {
	role, ok := s.requireWorkspaceRole(w, r, workspaceId)
	if !ok {
		return
	}
	// Admins remove anyone; everyone can leave.
	if role != roleAdmin && userId != currentSession(r).User.ID {
		forbidden(w)
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		member, err := lockedMember(ctx, q, workspaceId, userId)
		if err != nil {
			return err
		}
		if member.Role == roleAdmin {
			if err := requireAnotherAdmin(ctx, q, workspaceId); err != nil {
				return err
			}
		}
		// Out of every team first, so the teams' member lists stay right.
		teams, err := q.ListMemberTeams(ctx, dbgen.ListMemberTeamsParams{WorkspaceID: workspaceId, UserID: userId})
		if err != nil {
			return err
		}
		for _, team := range teams {
			if _, err := setTeamMember(ctx, q, team, userId, false); err != nil {
				return err
			}
		}
		// What was theirs alone goes with them. What they wrote for others
		// (issues, comments, shared views) stays.
		scope := func(run func() error) {
			if err == nil {
				err = run()
			}
		}
		scope(func() error {
			if err := q.DeleteMemberImportConnections(ctx, dbgen.DeleteMemberImportConnectionsParams{WorkspaceID: workspaceId, UserID: userId}); err != nil {
				return err
			}
			return q.DeleteMemberAPIKeys(ctx, dbgen.DeleteMemberAPIKeysParams{WorkspaceID: workspaceId, UserID: userId})
		})
		scope(func() error {
			return q.DeleteMemberNotifications(ctx, dbgen.DeleteMemberNotificationsParams{WorkspaceID: workspaceId, UserID: userId})
		})
		scope(func() error {
			return q.DeleteMemberFavorites(ctx, dbgen.DeleteMemberFavoritesParams{WorkspaceID: workspaceId, UserID: userId})
		})
		scope(func() error {
			return q.DeleteMemberPrivateViews(ctx, dbgen.DeleteMemberPrivateViewsParams{WorkspaceID: workspaceId, OwnerID: userId})
		})
		if err != nil {
			return err
		}
		if _, err := q.RemoveWorkspaceMember(ctx, dbgen.RemoveWorkspaceMemberParams{WorkspaceID: workspaceId, UserID: userId}); err != nil {
			return err
		}
		// Seeing their own record deleted is what ends the member's sync sockets.
		ack.SyncId, err = record(ctx, q, workspaceId, api.SyncModelUser, userId, syncer.Delete, userRecord(member.User, member.Role, member.JoinedAt), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

// lockedMember loads a workspace member after locking the workspace's
// membership, so that what the caller decides from it still holds at commit.
func lockedMember(ctx context.Context, q *dbgen.Queries, workspaceID, userID uuid.UUID) (dbgen.GetWorkspaceMemberRow, error) {
	if err := q.LockWorkspace(ctx, workspaceID); err != nil {
		return dbgen.GetWorkspaceMemberRow{}, err
	}
	member, err := q.GetWorkspaceMember(ctx, dbgen.GetWorkspaceMemberParams{WorkspaceID: workspaceID, UserID: userID})
	if db.IsNoRows(err) {
		return member, errNotMember
	}
	return member, err
}

// requireAnotherAdmin refuses to take away the workspace's only admin.
func requireAnotherAdmin(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID) error {
	admins, err := q.CountWorkspaceAdmins(ctx, workspaceID)
	if err != nil {
		return err
	}
	if admins <= 1 {
		return errLastAdmin
	}
	return nil
}
