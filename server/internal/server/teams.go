package server

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
)

var teamKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,6}$`)

func (s *Server) ListTeams(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	role, ok := s.requireWorkspaceRole(w, r, workspaceId)
	if !ok {
		return
	}
	rows, err := s.q.ListTeamsForUser(r.Context(), dbgen.ListTeamsForUserParams{
		WorkspaceID: workspaceId,
		UserID:      currentSession(r).User.ID,
		MembersOnly: role == roleGuest,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.Team, 0, len(rows))
	for _, row := range rows {
		out = append(out, toTeam(row.Team, row.IsMember))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreateTeam(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	role, ok := s.requireWorkspaceRole(w, r, workspaceId)
	if !ok {
		return
	}
	if role == roleGuest {
		forbidden(w)
		return
	}
	var req api.CreateTeamRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_name", fmt.Sprintf("Team name must be 1-%d characters.", maxNameLen))
		return
	}
	key := strings.ToUpper(strings.TrimSpace(req.Key))
	if !teamKeyPattern.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid_key", "Team identifier must be 1-7 letters or digits, starting with a letter.")
		return
	}
	kind := api.TeamKindEngineering
	if req.Kind != nil {
		kind = *req.Kind
	}
	if _, ok := teamWorkflows[kind]; !ok {
		writeError(w, http.StatusBadRequest, "invalid_kind", "That isn't a kind of team.")
		return
	}
	ctx := r.Context()
	userID := currentSession(r).User.ID

	var team dbgen.Team
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		team, err = createTeam(ctx, q, dbgen.CreateTeamParams{
			ID:          newID(),
			WorkspaceID: workspaceId,
			Name:        name,
			Key:         key,
			Private:     req.Private != nil && *req.Private,
			Kind:        string(kind),
		}, userID)
		return err
	})
	if db.IsUniqueViolation(err) {
		writeError(w, http.StatusConflict, "team_key_taken", "Another team already uses that identifier.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTeam(team, true))
}

func (s *Server) ListTeamMembers(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	if _, ok := s.requireTeamAccess(w, r, teamId); !ok {
		return
	}
	rows, err := s.q.ListTeamMembers(r.Context(), teamId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, toUser(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) AddTeamMember(w http.ResponseWriter, r *http.Request, teamId api.TeamId, userId uuid.UUID) {
	access, ok := s.requireTeamAccess(w, r, teamId)
	if !ok {
		return
	}
	self := userId == currentSession(r).User.ID
	// Members can add anyone, admins can manage any team they can see, and
	// anyone who can see a team may join it themselves. Guests are in the
	// teams they were put in, and don't put others in.
	if (!access.isMember && access.role != roleAdmin || access.role == roleGuest) && !self {
		forbidden(w)
		return
	}
	ctx := r.Context()
	if !self {
		_, err := s.q.GetWorkspaceRole(ctx, dbgen.GetWorkspaceRoleParams{WorkspaceID: access.team.WorkspaceID, UserID: userId})
		if db.IsNoRows(err) {
			writeError(w, http.StatusNotFound, "not_found", "That user is not a member of this workspace.")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		_, err := setTeamMember(ctx, q, access.team, userId, true)
		return err
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) RemoveTeamMember(w http.ResponseWriter, r *http.Request, teamId api.TeamId, userId uuid.UUID) {
	access, ok := s.requireTeamAccess(w, r, teamId)
	if !ok {
		return
	}
	if userId != currentSession(r).User.ID && access.role != roleAdmin {
		forbidden(w)
		return
	}
	ctx := r.Context()
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		_, err := setTeamMember(ctx, q, access.team, userId, false)
		return err
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type teamAccess struct {
	team     dbgen.Team
	role     string // caller's workspace role
	isMember bool   // caller belongs to the team
}

// requireTeamAccess loads a team the caller is allowed to see: one they belong
// to, or any public team unless they are a guest. Everything else is a 404.
func (s *Server) requireTeamAccess(w http.ResponseWriter, r *http.Request, teamID uuid.UUID) (teamAccess, bool) {
	ctx := r.Context()
	var access teamAccess

	team, err := s.q.GetTeam(ctx, teamID)
	if db.IsNoRows(err) {
		notFound(w)
		return access, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return access, false
	}
	role, ok := s.requireWorkspaceRole(w, r, team.WorkspaceID)
	if !ok {
		return access, false
	}
	isMember, err := s.q.IsTeamMember(ctx, dbgen.IsTeamMemberParams{TeamID: teamID, UserID: currentSession(r).User.ID})
	if err != nil {
		s.internalError(w, r, err)
		return access, false
	}
	if !isMember && (team.Private || role == roleGuest) {
		notFound(w)
		return access, false
	}
	return teamAccess{team: team, role: role, isMember: isMember}, true
}
