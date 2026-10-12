package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
)

const (
	sessionCookie = "cruuze_session"
	sessionTTL    = 30 * 24 * time.Hour
	// Sessions are extended at most this often, to avoid a write per request.
	sessionRefreshAfter = time.Hour
)

type session struct {
	ID   uuid.UUID
	User dbgen.User
	// tokenHash lets long-lived connections check the session still exists.
	tokenHash []byte
	// keyWorkspace is set when the request carried an API key instead of a
	// session cookie: the one workspace the key may touch.
	keyWorkspace *uuid.UUID
}

// allows reports whether the session may touch the workspace at all. Browser
// sessions may (membership is checked separately); an API key only its own.
func (s session) allows(workspaceID uuid.UUID) bool {
	return s.keyWorkspace == nil || *s.keyWorkspace == workspaceID
}

type sessionKey struct{}

// currentSession returns the authenticated session. It must only be called
// from handlers of operations that require cookieAuth.
func currentSession(r *http.Request) session {
	return r.Context().Value(sessionKey{}).(session)
}

// authenticate enforces the session cookie on operations whose OpenAPI
// definition requires cookieAuth; other operations pass through.
func (s *Server) authenticate(next http.Handler) http.Handler {
	required := s.requireSession(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(api.CookieAuthScopes) == nil {
			next.ServeHTTP(w, r)
			return
		}
		required.ServeHTTP(w, r)
	})
}

// requireSession rejects requests without a valid session cookie or API key
// and makes the session available through currentSession.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			sess, ok := s.apiKeySession(w, r, strings.TrimSpace(token))
			if ok {
				s.known.add(sess.tokenHash)
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
			}
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
			return
		}
		tokenHash := auth.HashToken(c.Value)
		row, err := s.q.GetSessionUser(r.Context(), tokenHash)
		if db.IsNoRows(err) {
			s.clearSessionCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if time.Since(row.LastSeenAt) > sessionRefreshAfter {
			expires := time.Now().Add(sessionTTL)
			if err := s.q.TouchSession(r.Context(), dbgen.TouchSessionParams{ID: row.SessionID, ExpiresAt: expires}); err != nil {
				s.log.Warn("extend session", "err", err)
			} else {
				s.setSessionCookie(w, c.Value)
			}
		}
		s.known.add(tokenHash)
		sess := session{ID: row.SessionID, User: row.User, tokenHash: tokenHash}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}

// apiKeySession authenticates a request by its API key. The key acts as its
// owner for as long as they belong to the key's workspace.
func (s *Server) apiKeySession(w http.ResponseWriter, r *http.Request, token string) (session, bool) {
	tokenHash := auth.HashToken(token)
	row, err := s.q.GetAPIKeyUser(r.Context(), tokenHash)
	if db.IsNoRows(err) || (err == nil && !strings.HasPrefix(token, apiKeyPrefix)) {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "That API key is not valid.")
		return session{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return session{}, false
	}
	if row.LastUsedAt == nil || time.Since(*row.LastUsedAt) > sessionRefreshAfter {
		if err := s.q.TouchAPIKey(r.Context(), row.KeyID); err != nil {
			s.log.Warn("touch api key", "err", err)
		}
	}
	return session{ID: row.KeyID, User: row.User, tokenHash: tokenHash, keyWorkspace: &row.WorkspaceID}, true
}

// stillValid reports whether a session (or API key) seen earlier still exists.
func (s *Server) stillValid(ctx context.Context, sess session) bool {
	var err error
	if sess.keyWorkspace != nil {
		_, err = s.q.GetAPIKeyUser(ctx, sess.tokenHash)
	} else {
		_, err = s.q.GetSessionUser(ctx, sess.tokenHash)
	}
	return err == nil
}

// requireBrowser rejects API keys on endpoints that manage the account
// itself, which a key must not be able to do.
func (s *Server) requireBrowser(w http.ResponseWriter, r *http.Request) bool {
	if currentSession(r).keyWorkspace != nil {
		writeError(w, http.StatusForbidden, "forbidden", "API keys can't do that; sign in instead.")
		return false
	}
	return true
}

// startSession creates a session for the user and sets its cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	token := auth.NewToken()
	userAgent := r.UserAgent()
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	err := s.q.CreateSession(r.Context(), dbgen.CreateSessionParams{
		ID:        newID(),
		UserID:    userID,
		TokenHash: auth.HashToken(token),
		UserAgent: userAgent,
		ExpiresAt: time.Now().Add(sessionTTL),
	})
	if err != nil {
		return err
	}
	s.known.add(auth.HashToken(token))
	s.setSessionCookie(w, token)
	return nil
}

// credentialMemory is how long a credential stays in a credentialSet after
// it last proved valid.
const credentialMemory = 10 * time.Minute

// credentialSet remembers, by hash, the session tokens and API keys that
// authenticated a request lately. It only decides how a request is counted
// by the rate limiter, never whether it is allowed.
type credentialSet struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	pruned time.Time
}

func (c *credentialSet) add(hash []byte) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]time.Time{}
	}
	c.seen[string(hash)] = now
	if now.Sub(c.pruned) > credentialMemory {
		for k, at := range c.seen {
			if now.Sub(at) > credentialMemory {
				delete(c.seen, k)
			}
		}
		c.pruned = now
	}
}

func (c *credentialSet) has(hash []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.seen[string(hash)]
	return ok && time.Since(at) <= credentialMemory
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

// newID returns a time-ordered UUID, which keeps primary-key indexes compact.
func newID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
