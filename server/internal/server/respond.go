package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
)

const maxBodyBytes = 1 << 20

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // client went away
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, api.Error{Code: code, Message: message})
}

// internalError logs err and responds with an opaque 500.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
}

// apiError is an error with a response the client should see. Returning one
// from inside a transaction rolls it back and reports the error.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

func badRequest(code, message string) *apiError {
	return &apiError{status: http.StatusBadRequest, code: code, message: message}
}

// fail responds with err's own response if it is an apiError, else an opaque 500.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		writeError(w, apiErr.status, apiErr.code, apiErr.message)
		return
	}
	s.internalError(w, r, err)
}

func notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Not found.")
}

func forbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "forbidden", "You don't have permission to do that.")
}

// decodeJSON reads the request body into v, writing a 400 and returning false on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Request body is too large.")
		} else {
			writeError(w, http.StatusBadRequest, "bad_request", "Request body is not valid JSON.")
		}
		return false
	}
	return true
}

func toUser(u dbgen.User) api.User {
	out := api.User{Id: u.ID, Email: u.Email, Name: u.Name, CreatedAt: u.CreatedAt}
	if u.AvatarUrl != "" {
		out.AvatarUrl = &u.AvatarUrl
	}
	return out
}

func toWorkspace(w dbgen.Workspace, role string) api.Workspace {
	return api.Workspace{
		Id:                w.ID,
		Name:              w.Name,
		UrlKey:            w.UrlKey,
		Role:              api.WorkspaceRole(role),
		PortalEnabled:     w.PortalEnabled,
		PortalPublicBoard: w.PortalPublicBoard,
		PortalIntro:       w.PortalIntro,
		CreatedAt:         w.CreatedAt,
	}
}

func toTeam(t dbgen.Team, isMember bool) api.Team {
	return api.Team{
		Id:          t.ID,
		WorkspaceId: t.WorkspaceID,
		Name:        t.Name,
		Key:         t.Key,
		Private:     t.Private,
		IsMember:    isMember,
		CreatedAt:   t.CreatedAt,
	}
}

func toInvite(i dbgen.WorkspaceInvite) api.WorkspaceInvite {
	return api.WorkspaceInvite{
		Id:        i.ID,
		Email:     i.Email,
		Role:      api.WorkspaceRole(i.Role),
		CreatedAt: i.CreatedAt,
	}
}
