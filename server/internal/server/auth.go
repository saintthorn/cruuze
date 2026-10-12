package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/config"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	mailer "cruuze/server/internal/mail"
)

const (
	loginTokenTTL = 15 * time.Minute
	// Max magic links per email address within loginTokenTTL.
	loginTokenLimit = 5

	oauthStateCookie = "cruuze_oauth"
)

func (s *Server) GetAuthConfig(w http.ResponseWriter, r *http.Request) {
	settings, err := s.q.GetInstanceSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.AuthConfig{Google: s.google != nil, Signup: api.SignupMode(settings.Signup), Version: config.Version})
}

func (s *Server) RequestMagicLink(w http.ResponseWriter, r *http.Request) {
	var req api.MagicLinkRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}
	ctx := r.Context()

	recent, err := s.q.CountLoginTokensSince(ctx, dbgen.CountLoginTokensSinceParams{
		Email:     email,
		CreatedAt: time.Now().Add(-loginTokenTTL),
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if recent >= loginTokenLimit {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many sign-in emails requested. Try again later.")
		return
	}

	// A customer signing in to a workspace's portal is let in whatever the
	// signup rules: they get a portal, not the app.
	var portal *dbgen.Workspace
	if req.Portal != nil && urlKeyPattern.MatchString(*req.Portal) {
		if ws, err := s.q.GetWorkspaceByURLKey(ctx, *req.Portal); err == nil && ws.PortalEnabled {
			portal = &ws
		}
	}
	if portal == nil {
		allowed, err := s.allowSignup(ctx, s.q, email)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if !allowed {
			s.fail(w, r, errSignupClosed)
			return
		}
	}

	token := auth.NewToken()
	err = s.q.CreateLoginToken(ctx, dbgen.CreateLoginTokenParams{
		ID:        newID(),
		Email:     email,
		TokenHash: auth.HashToken(token),
		ExpiresAt: time.Now().Add(loginTokenTTL),
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// The token travels in the URL fragment so it never reaches server logs,
	// and the page exchanges it with a POST so link prefetchers can't consume it.
	link := s.cfg.AppURL + "/auth/verify#token=" + token
	subject, where := "Sign in to Cruuze", "Cruuze"
	if portal != nil {
		// Asked for from a workspace's portal: the link leads back there.
		link = s.cfg.AppURL + "/auth/verify?next=" + url.QueryEscape("/portal/"+portal.UrlKey) + "#token=" + token
		subject, where = "Sign in to "+portal.Name+" requests", portal.Name+"'s request page"
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err = s.mail.Send(sendCtx, mailer.Message{
		To:      email,
		Subject: subject,
		Text: fmt.Sprintf("Click the link below to sign in to %s. It expires in %d minutes and can be used once.\n\n%s\n\nIf you didn't request this, you can ignore this email.\n",
			where, int(loginTokenTTL.Minutes()), link),
	})
	if err != nil {
		s.log.Error("send magic link", "err", err)
		writeError(w, http.StatusBadGateway, "email_failed", "We couldn't send the sign-in email. Try again.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) VerifyMagicLink(w http.ResponseWriter, r *http.Request) {
	var req api.VerifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()

	email, err := s.q.ConsumeLoginToken(ctx, auth.HashToken(req.Token))
	if db.IsNoRows(err) {
		writeError(w, http.StatusBadRequest, "invalid_token", "This sign-in link is invalid or has expired.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	var user dbgen.User
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		user, err = signInByEmail(ctx, q, email)
		return err
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.startSession(w, r, user.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeMe(w, r, user, nil)
}

func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	if !s.requireBrowser(w, r) {
		return
	}
	if err := s.q.DeleteSession(r.Context(), currentSession(r).ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// googleStart redirects the browser to Google's consent screen.
func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		notFound(w)
		return
	}
	state, verifier := auth.NewToken(), oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state + "." + verifier,
		Path:     "/api/auth/google",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.google.AuthURL(state, verifier), http.StatusFound)
}

// googleCallback completes Google sign-in and redirects back to the app.
func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		notFound(w)
		return
	}
	fail := func(reason string, err error) {
		s.log.Warn("google sign-in failed", "reason", reason, "err", err)
		http.Redirect(w, r, s.cfg.AppURL+"/login?error=google", http.StatusFound)
	}

	c, err := r.Cookie(oauthStateCookie)
	if err != nil {
		fail("missing state cookie", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/api/auth/google", MaxAge: -1})
	state, verifier, ok := strings.Cut(c.Value, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
		fail("state mismatch", nil)
		return
	}

	ctx := r.Context()
	identity, err := s.google.Exchange(ctx, r.URL.Query().Get("code"), verifier)
	if err != nil {
		fail("exchange", err)
		return
	}
	email, ok := normalizeEmail(identity.Email)
	if !ok {
		fail("invalid email from provider", nil)
		return
	}

	var user dbgen.User
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		user, err = q.GetOAuthAccountUser(ctx, dbgen.GetOAuthAccountUserParams{
			Provider:       "google",
			ProviderUserID: identity.Subject,
		})
		if db.IsNoRows(err) {
			allowed, err := s.allowSignup(ctx, q, email)
			if err != nil {
				return err
			}
			if !allowed {
				return errSignupClosed
			}
			// First Google sign-in: link to the account with this verified email.
			if user, err = signInByEmail(ctx, q, email); err != nil {
				return err
			}
			err = q.CreateOAuthAccount(ctx, dbgen.CreateOAuthAccountParams{
				Provider:       "google",
				ProviderUserID: identity.Subject,
				UserID:         user.ID,
			})
		}
		if err != nil {
			return err
		}
		before := user
		user, err = q.FillUserProfile(ctx, dbgen.FillUserProfileParams{
			ID:        user.ID,
			Name:      truncate(strings.TrimSpace(identity.Name), maxNameLen),
			AvatarUrl: identity.Picture,
		})
		if err != nil || (user.Name == before.Name && user.AvatarUrl == before.AvatarUrl) {
			return err
		}
		return recordUserChanged(ctx, q, user)
	})
	if errors.Is(err, errSignupClosed) {
		// Not a failure of Google's: this server takes invited people only.
		s.log.Info("google sign-in refused: signups are closed")
		http.Redirect(w, r, s.cfg.AppURL+"/login?error=signup_closed", http.StatusFound)
		return
	}
	if err != nil {
		fail("store user", err)
		return
	}
	if err := s.startSession(w, r, user.ID); err != nil {
		fail("start session", err)
		return
	}
	http.Redirect(w, r, s.cfg.AppURL+"/", http.StatusFound)
}

// signInByEmail finds or creates the user who proved ownership of email and
// accepts any workspace invites waiting for that address.
func signInByEmail(ctx context.Context, q *dbgen.Queries, email string) (dbgen.User, error) {
	user, err := q.GetOrCreateUserByEmail(ctx, dbgen.GetOrCreateUserByEmailParams{ID: newID(), Email: email})
	if err != nil {
		return user, err
	}
	// Whoever signs in first on a new install runs it.
	if claimed, err := q.ClaimInstanceAdmin(ctx, user.ID); err != nil {
		return user, err
	} else if claimed > 0 {
		user.InstanceAdmin = true
	}
	joined, err := q.AcceptInvitesForEmail(ctx, dbgen.AcceptInvitesForEmailParams{Email: email, UserID: user.ID})
	if err != nil {
		return user, err
	}
	// Take the workspace locks in a stable order; see recordUserChanged.
	slices.SortFunc(joined, func(a, b dbgen.AcceptInvitesForEmailRow) int {
		return bytes.Compare(a.WorkspaceID[:], b.WorkspaceID[:])
	})
	for _, m := range joined {
		if err := recordUserJoined(ctx, q, m.WorkspaceID, user, m.Role, m.JoinedAt); err != nil {
			return user, err
		}
	}
	return user, nil
}

// normalizeEmail lowercases and validates a bare email address.
func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > 254 {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return "", false
	}
	return email, true
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}
