package server

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	mailer "cruuze/server/internal/mail"
)

const maxNameLen = 80

const (
	roleAdmin  = string(api.WorkspaceRoleAdmin)
	roleMember = string(api.WorkspaceRoleMember)
	roleGuest  = string(api.WorkspaceRoleGuest)
)

// GitHub usernames: letters, digits and single hyphens, up to 39 characters.
// Empty clears the setting.
var githubLoginPattern = regexp.MustCompile(`^([A-Za-z0-9](-?[A-Za-z0-9]){0,38})?$`)

var urlKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

// Workspace URL keys are the first path segment of the web app, so they must
// not collide with its own top-level routes.
var reservedURLKeys = map[string]bool{
	"api": true, "assets": true, "auth": true, "join": true, "login": true,
	"logout": true, "settings": true, "healthz": true, "new": true, "portal": true,
}

func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	s.writeMe(w, r, currentSession(r).User, currentSession(r).keyWorkspace)
}

// writeMe responds with the user and their workspaces; only, if set, limits
// them to that one workspace (for API keys).
func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, user dbgen.User, only *uuid.UUID) {
	rows, err := s.q.ListWorkspacesForUser(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	canCreate, err := s.mayCreateWorkspace(r.Context(), s.q, user)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	me := api.Me{
		User: toUser(user), EmailNotifications: user.EmailNotifications, GithubLogin: user.GithubLogin,
		InstanceAdmin: s.isInstanceAdmin(user), CanCreateWorkspace: canCreate,
		Workspaces: make([]api.Workspace, 0, len(rows)),
	}
	for _, row := range rows {
		if only != nil && *only != row.Workspace.ID {
			continue
		}
		me.Workspaces = append(me.Workspaces, toWorkspace(row.Workspace, row.Role))
	}
	writeJSON(w, http.StatusOK, me)
}

func (s *Server) UpdateMe(w http.ResponseWriter, r *http.Request) {
	if !s.requireBrowser(w, r) {
		return
	}
	var req api.UpdateMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var name string
	if req.Name != nil {
		var ok bool
		if name, ok = cleanName(*req.Name); !ok {
			writeError(w, http.StatusBadRequest, "invalid_name", fmt.Sprintf("Name must be 1-%d characters.", maxNameLen))
			return
		}
	}
	if req.GithubLogin != nil && !githubLoginPattern.MatchString(*req.GithubLogin) {
		writeError(w, http.StatusBadRequest, "invalid_github_login", "That doesn't look like a GitHub username.")
		return
	}
	ctx := r.Context()
	user := currentSession(r).User
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		if req.GithubLogin != nil {
			user, err = q.UpdateUserGithubLogin(ctx, dbgen.UpdateUserGithubLoginParams{ID: user.ID, GithubLogin: *req.GithubLogin})
			if err != nil {
				return err
			}
		}
		if req.EmailNotifications != nil {
			// A private preference: other members' copies of the user don't change.
			user, err = q.UpdateUserEmailNotifications(ctx, dbgen.UpdateUserEmailNotificationsParams{ID: user.ID, EmailNotifications: *req.EmailNotifications})
			if err != nil {
				return err
			}
		}
		if req.Name == nil {
			return nil
		}
		user, err = q.UpdateUserName(ctx, dbgen.UpdateUserNameParams{ID: user.ID, Name: name})
		if err != nil {
			return err
		}
		return recordUserChanged(ctx, q, user)
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUser(user))
}

func (s *Server) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.requireBrowser(w, r) {
		return
	}
	var req api.CreateWorkspaceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_name", fmt.Sprintf("Workspace name must be 1-%d characters.", maxNameLen))
		return
	}
	urlKey := strings.ToLower(strings.TrimSpace(req.UrlKey))
	if !urlKeyPattern.MatchString(urlKey) || reservedURLKeys[urlKey] {
		writeError(w, http.StatusBadRequest, "invalid_url_key", "Workspace URL must be 3-32 lowercase letters, digits or dashes.")
		return
	}
	ctx := r.Context()
	creator := currentSession(r).User
	if allowed, err := s.mayCreateWorkspace(ctx, s.q, creator); err != nil {
		s.internalError(w, r, err)
		return
	} else if !allowed {
		writeError(w, http.StatusForbidden, "workspace_creation_closed", "Only this server's admins can create workspaces.")
		return
	}

	var ws dbgen.Workspace
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		ws, err = q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{ID: newID(), Name: name, UrlKey: urlKey})
		if err != nil {
			return err
		}
		err = q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{WorkspaceID: ws.ID, UserID: creator.ID, Role: roleAdmin})
		if err != nil {
			return err
		}
		if err := recordUserJoined(ctx, q, ws.ID, creator, roleAdmin, ws.CreatedAt); err != nil {
			return err
		}
		// Every workspace starts with one team so there is somewhere to file issues.
		_, err = createTeam(ctx, q, dbgen.CreateTeamParams{
			ID:          newID(),
			WorkspaceID: ws.ID,
			Name:        name,
			Key:         deriveTeamKey(name),
		}, creator.ID)
		return err
	})
	if db.IsUniqueViolation(err) {
		writeError(w, http.StatusConflict, "url_key_taken", "That workspace URL is already taken.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toWorkspace(ws, roleAdmin))
}

func (s *Server) ListWorkspaceMembers(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if _, ok := s.requireWorkspaceRole(w, r, workspaceId); !ok {
		return
	}
	rows, err := s.q.ListWorkspaceMembers(r.Context(), workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.WorkspaceMember, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.WorkspaceMember{
			User:     toUser(row.User),
			Role:     api.WorkspaceRole(row.Role),
			JoinedAt: row.JoinedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ListWorkspaceInvites(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	rows, err := s.q.ListWorkspaceInvites(r.Context(), workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.WorkspaceInvite, 0, len(rows))
	for _, row := range rows {
		out = append(out, toInvite(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreateWorkspaceInvite(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	var req api.CreateInviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}
	role := roleMember
	if req.Role != nil {
		role = string(*req.Role)
	}
	if role != roleAdmin && role != roleMember && role != roleGuest {
		writeError(w, http.StatusBadRequest, "invalid_role", "Role must be admin, member or guest.")
		return
	}
	ctx := r.Context()
	inviter := currentSession(r).User

	isMember, err := s.q.IsWorkspaceMemberEmail(ctx, dbgen.IsWorkspaceMemberEmailParams{WorkspaceID: workspaceId, Email: email})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if isMember {
		writeError(w, http.StatusConflict, "already_member", "That person is already a member of this workspace.")
		return
	}

	var invite dbgen.WorkspaceInvite
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		invite, err = q.CreateWorkspaceInvite(ctx, dbgen.CreateWorkspaceInviteParams{
			ID:          newID(),
			WorkspaceID: workspaceId,
			Email:       email,
			Role:        role,
			InvitedBy:   &inviter.ID,
		})
		return err
	})
	if db.IsUniqueViolation(err) {
		writeError(w, http.StatusConflict, "already_invited", "That email has already been invited.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// The invite is accepted whenever that address signs in, so a failed
	// email is not fatal: the admin can share the login link themselves.
	go s.sendInviteEmail(email, displayName(inviter))

	writeJSON(w, http.StatusCreated, toInvite(invite))
}

func (s *Server) sendInviteEmail(to, inviterName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := s.mail.Send(ctx, mailer.Message{
		To:      to,
		Subject: inviterName + " invited you to Cruuze",
		Text: fmt.Sprintf("%s invited you to their workspace on Cruuze.\n\nSign in with this email address to join:\n\n%s/login\n",
			inviterName, s.cfg.AppURL),
	})
	if err != nil {
		s.log.Error("send invite email", "err", err)
	}
}

func (s *Server) DeleteWorkspaceInvite(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId, inviteId uuid.UUID) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	n, err := s.q.DeleteWorkspaceInvite(r.Context(), dbgen.DeleteWorkspaceInviteParams{ID: inviteId, WorkspaceID: workspaceId})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if n == 0 {
		notFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireWorkspaceRole returns the caller's role in the workspace. Non-members
// get a 404 so workspace IDs can't be probed, and so does an API key that
// belongs to another workspace.
func (s *Server) requireWorkspaceRole(w http.ResponseWriter, r *http.Request, workspaceID uuid.UUID) (string, bool) {
	if !currentSession(r).allows(workspaceID) {
		notFound(w)
		return "", false
	}
	role, err := s.q.GetWorkspaceRole(r.Context(), dbgen.GetWorkspaceRoleParams{
		WorkspaceID: workspaceID,
		UserID:      currentSession(r).User.ID,
	})
	if db.IsNoRows(err) {
		notFound(w)
		return "", false
	}
	if err != nil {
		s.internalError(w, r, err)
		return "", false
	}
	return role, true
}

func (s *Server) requireWorkspaceAdmin(w http.ResponseWriter, r *http.Request, workspaceID uuid.UUID) bool {
	role, ok := s.requireWorkspaceRole(w, r, workspaceID)
	if !ok {
		return false
	}
	if role != roleAdmin {
		forbidden(w)
		return false
	}
	return true
}

// cleanName trims a display name and checks its length.
func cleanName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= maxNameLen
}

func displayName(u dbgen.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// deriveTeamKey builds an issue prefix from the first three letters or digits
// of name, e.g. "Acme Inc" -> "ACM".
func deriveTeamKey(name string) string {
	var key []rune
	for _, c := range strings.ToUpper(name) {
		isLetter := c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		if isLetter || (isDigit && len(key) > 0) {
			key = append(key, c)
		}
		if len(key) == 3 {
			break
		}
	}
	if len(key) == 0 {
		return "TEAM"
	}
	return string(key)
}
