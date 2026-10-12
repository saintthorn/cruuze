package server

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/db/dbgen"
)

const (
	// apiKeyPrefix makes keys recognizable, e.g. to secret scanners.
	apiKeyPrefix = "cruuze_"
	maxAPIKeys   = 50
)

func toAPIKey(k dbgen.ApiKey) api.ApiKey {
	return api.ApiKey{Id: k.ID, Name: k.Name, Hint: k.Hint, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt}
}

func (s *Server) ListApiKeys(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireBrowser(w, r) {
		return
	}
	if _, ok := s.requireWorkspaceRole(w, r, workspaceId); !ok {
		return
	}
	keys, err := s.q.ListAPIKeys(r.Context(), dbgen.ListAPIKeysParams{WorkspaceID: workspaceId, UserID: currentSession(r).User.ID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.ApiKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, toAPIKey(k))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreateApiKey(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireBrowser(w, r) {
		return
	}
	if _, ok := s.requireWorkspaceRole(w, r, workspaceId); !ok {
		return
	}
	var req api.CreateApiKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_name", fmt.Sprintf("Key name must be 1-%d characters.", maxNameLen))
		return
	}
	ctx := r.Context()
	userID := currentSession(r).User.ID

	existing, err := s.q.ListAPIKeys(ctx, dbgen.ListAPIKeysParams{WorkspaceID: workspaceId, UserID: userID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(existing) >= maxAPIKeys {
		writeError(w, http.StatusBadRequest, "too_many_keys", "You have too many API keys. Delete one first.")
		return
	}

	// Only the hash is stored: the key can't be shown again.
	token := apiKeyPrefix + auth.NewToken()
	key, err := s.q.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{
		ID:          newID(),
		WorkspaceID: workspaceId,
		UserID:      userID,
		Name:        name,
		TokenHash:   auth.HashToken(token),
		Hint:        token[len(token)-4:],
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.CreatedApiKey{ApiKey: toAPIKey(key), Token: token})
}

func (s *Server) DeleteApiKey(w http.ResponseWriter, r *http.Request, apiKeyId uuid.UUID) {
	if !s.requireBrowser(w, r) {
		return
	}
	n, err := s.q.DeleteAPIKey(r.Context(), dbgen.DeleteAPIKeyParams{ID: apiKeyId, UserID: currentSession(r).User.ID})
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
