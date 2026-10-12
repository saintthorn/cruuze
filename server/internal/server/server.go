// Package server implements the HTTP API described by api/openapi.yaml.
package server

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/blob"
	"cruuze/server/internal/config"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/mail"
	"cruuze/server/internal/seal"
	"cruuze/server/internal/syncer"
)

type Server struct {
	cfg    config.Config
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	mail   mail.Sender
	google *auth.Google // nil when Google sign-in is not configured
	hub    *syncer.Hub
	// blobs holds attachment bytes.
	blobs blob.Store
	// outbound calls URLs supplied by users (webhooks).
	outbound *http.Client
	// importHTTP reads other trackers on behalf of importers.
	importHTTP *http.Client
	// sealKey encrypts the import credentials that are kept; nil keeps none.
	sealKey *seal.Key
	// imports counts the imports in flight, so tests can wait for them.
	imports sync.WaitGroup
	metrics *serverMetrics
	// known holds the credentials seen to be valid lately; see limitRequests.
	known credentialSet
	// closing is closed when the server starts shutting down, to end
	// long-lived connections that http.Server.Shutdown doesn't wait for.
	closing chan struct{}
	log     *slog.Logger
	// webFS is the built web app, or nil when the binary was built without it.
	webFS fs.FS
}

var _ api.ServerInterface = (*Server)(nil)

func New(cfg config.Config, pool *pgxpool.Pool, sender mail.Sender, blobs blob.Store, log *slog.Logger, webFS fs.FS) *Server {
	s := &Server{
		cfg:        cfg,
		pool:       pool,
		q:          dbgen.New(pool),
		mail:       sender,
		hub:        syncer.NewHub(),
		blobs:      blobs,
		outbound:   newOutboundClient(cfg.AllowPrivateWebhooks),
		importHTTP: newImportClient(cfg.AllowPrivateImports),
		metrics:    newServerMetrics(pool),
		closing:    make(chan struct{}),
		log:        log,
		webFS:      webFS,
	}
	if cfg.GoogleEnabled() {
		s.google = auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.AppURL+"/api/auth/google/callback")
	}
	if cfg.SecretsKey != "" {
		key, err := seal.ParseKey(cfg.SecretsKey)
		if err != nil {
			panic(err) // SecretsKey is validated in config.Load
		}
		s.sealKey = key
	}
	return s
}

// Hub returns the hub that wakes sync sockets. The caller is responsible for
// feeding it, normally by running Hub().Listen.
func (s *Server) Hub() *syncer.Hub { return s.hub }

// Close ends the server's WebSocket connections; clients reconnect on their
// own. Call it once, before shutting down the HTTP server.
func (s *Server) Close() { close(s.closing) }

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		// Behind a proxy every request appears to come from the proxy; take
		// the client's address from the header the proxy sets instead.
		r.Use(middleware.RealIP)
	} else {
		// With no proxy in front, a request ID in the request is the
		// client's own choice; it doesn't get to name its log lines.
		r.Use(dropRequestID)
	}
	r.Use(middleware.RequestID)
	r.Use(s.observe)
	r.Use(s.recoverPanics)
	r.Use(s.securityHeaders)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/readyz", s.ready)
	r.Get("/metrics", s.serveMetrics)

	// Responses that compress well: JSON from the API, and the app's files.
	compress := middleware.Compress(5, "application/json", "text/html", "text/css", "text/javascript", "application/javascript", "image/svg+xml")

	// Reject state-changing requests that browsers send from other origins.
	csrf := http.NewCrossOriginProtection()
	if err := csrf.AddTrustedOrigin(s.cfg.AppURL); err != nil {
		panic(err) // AppURL is validated in config.Load
	}
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "cross_origin", "Cross-origin request rejected.")
	}))

	r.Route("/api", func(r chi.Router) {
		r.Use(csrf.Handler)
		r.Use(noStore)
		r.Use(s.limitRequests())
		r.Use(limitAuthAttempts())

		r.Get("/auth/google/start", s.googleStart)
		r.Get("/auth/google/callback", s.googleCallback)
		r.With(s.requireSession).Get("/workspaces/{workspaceId}/sync/ws", s.syncSocket)
		// For assistants: the Model Context Protocol, reached with an API key.
		r.With(s.requireSession).Handle("/mcp", s.mcpHandler())
		// Connecting a tracker goes through its own sign-in page and back.
		r.With(s.requireSession).Get("/workspaces/{workspaceId}/imports/connect/{provider}", s.connectImportStart)
		r.With(s.requireSession).Get("/imports/connect/{provider}/callback", s.connectImportCallback)

		api.HandlerWithOptions(s, api.ChiServerOptions{
			BaseRouter:  r,
			Middlewares: []api.MiddlewareFunc{s.authenticate, compress},
			ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				writeError(w, http.StatusBadRequest, "bad_request", paramErrorMessage(err))
			},
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "No such endpoint.")
		})
	})

	if s.webFS != nil {
		r.NotFound(compress(http.HandlerFunc(s.serveWeb)).ServeHTTP)
	}
	return r
}

// dropRequestID removes a request ID the client sent, so a fresh one is made.
func dropRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(middleware.RequestIDHeader)
		next.ServeHTTP(w, r)
	})
}

// noStore keeps API responses out of caches unless a handler says otherwise.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// paramErrorMessage says which parameter of a request was malformed, without
// repeating the parser's own error.
func paramErrorMessage(err error) string {
	var format *api.InvalidParamFormatError
	var required *api.RequiredParamError
	switch {
	case errors.As(err, &format):
		return "Invalid parameter: " + format.ParamName + "."
	case errors.As(err, &required):
		return "Missing parameter: " + required.ParamName + "."
	}
	return "Invalid request."
}

// limitAuthAttempts throttles unauthenticated sign-in endpoints per client IP.
// Magic links are additionally limited per email address in the handler.
func limitAuthAttempts() func(http.Handler) http.Handler {
	limiter := httprate.Limit(30, time.Minute,
		httprate.WithKeyFuncs(httprate.KeyByIP),
		httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again in a minute.")
		}),
	)
	return func(next http.Handler) http.Handler {
		limited := limiter(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && (r.URL.Path == "/api/auth/magic-link" || r.URL.Path == "/api/auth/verify") {
				limited.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// limitRequests caps how fast one client can call the API: a guard against
// runaway scripts and scraping rather than a quota. Signed-in clients are
// counted by their credential, so people behind one address don't share a
// budget; everyone else is counted by address. Only a credential that has
// proved valid counts as signed in: otherwise a made-up one on each request
// would get a fresh budget every time.
func (s *Server) limitRequests() func(http.Handler) http.Handler {
	if s.cfg.RateLimit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	return httprate.Limit(s.cfg.RateLimit, time.Minute,
		httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
			if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
				if hash := auth.HashToken(strings.TrimSpace(token)); s.known.has(hash) {
					return "key:" + string(hash), nil
				}
			} else if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
				if hash := auth.HashToken(c.Value); s.known.has(hash) {
					return "session:" + string(hash), nil
				}
			}
			return "ip:" + clientIP(r), nil
		}),
		httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Slow down and try again in a minute.")
		}),
	)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if s.cfg.SecureCookies() {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy is sent with the web app. Scripts come only from the
// app's own files, so injected markup can't run code even if it got past
// the Markdown renderer. Styles allow inline because components set style
// attributes; images allow any https source for avatars and pasted links.
func (s *Server) contentSecurityPolicy() string {
	host := s.appHost()
	script, connect := "script-src 'self'", "connect-src 'self' ws://"+host+" wss://"+host
	if s.cfg.CloudflareAnalytics {
		script += " https://static.cloudflareinsights.com"
		connect += " https://cloudflareinsights.com"
	}
	return strings.Join([]string{
		"default-src 'self'",
		script,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"font-src 'self' data:",
		// 'self' doesn't cover WebSockets in every browser.
		connect,
		// The service worker that keeps the app's files for offline use.
		"worker-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// serveWeb serves the embedded web app, falling back to index.html so that
// client-side routes work on reload.
func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if info, err := fs.Stat(s.webFS, name); name == "" || err != nil || info.IsDir() {
		name = "index.html"
	}
	w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy())
	if strings.HasPrefix(name, "assets/") {
		// Vite fingerprints everything under assets/.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else if strings.HasPrefix(name, "fonts/") {
		// Not fingerprinted, but a typeface doesn't change under its name.
		w.Header().Set("Cache-Control", "public, max-age=2592000")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.webFS, name)
}
