package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"cruuze/server/internal/metrics"
)

// serverMetrics is what the server counts about itself.
type serverMetrics struct {
	registry *metrics.Registry
	requests *metrics.Counter
	duration *metrics.Histogram
	webhooks *metrics.Counter
	emails   *metrics.Counter
	// syncSockets is the number of open sync sockets.
	syncSockets atomic.Int64
}

func newServerMetrics(pool *pgxpool.Pool) *serverMetrics {
	r := metrics.NewRegistry()
	m := &serverMetrics{
		registry: r,
		requests: r.Counter("cruuze_http_requests_total", "HTTP requests handled.", "method", "route", "status"),
		duration: r.Histogram("cruuze_http_request_duration_seconds", "Time to handle an HTTP request.",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}, "method", "route"),
		webhooks: r.Counter("cruuze_webhook_deliveries_total", "Webhook delivery attempts.", "outcome"),
		emails:   r.Counter("cruuze_emails_total", "Emails handed to the mail service.", "outcome"),
	}
	r.Gauge("cruuze_sync_sockets", "Open sync WebSocket connections.", func() float64 { return float64(m.syncSockets.Load()) })
	r.Gauge("cruuze_db_connections", "Connections in the database pool.", func() float64 { return float64(pool.Stat().TotalConns()) })
	r.Gauge("cruuze_db_connections_in_use", "Database connections currently in use.", func() float64 { return float64(pool.Stat().AcquiredConns()) })
	r.Gauge("cruuze_db_connections_max", "Size limit of the database pool.", func() float64 { return float64(pool.Stat().MaxConns()) })
	return m
}

// quietPaths are probed constantly by the platform; logging them is noise.
var quietPaths = map[string]bool{"/healthz": true, "/readyz": true, "/metrics": true}

// observe logs each request and records it in the metrics. The request ID it
// logs is also sent back as X-Request-Id, to tie a user's report to a log line.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := middleware.GetReqID(r.Context())
		w.Header().Set("X-Request-Id", requestID)
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		elapsed := time.Since(start)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		route := routeOf(r)
		s.metrics.requests.Inc(r.Method, route, strconv.Itoa(status))
		// A sync socket stays open for hours; its "duration" isn't latency.
		if !strings.HasSuffix(route, "/sync/ws") {
			s.metrics.duration.Observe(elapsed.Seconds(), r.Method, route)
		}
		if quietPaths[r.URL.Path] {
			return
		}
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", elapsed,
			"request_id", requestID,
			"ip", clientIP(r),
		)
	})
}

// routeOf names the route that handled a request by its pattern, e.g.
// /api/issues/{issueId}, so that metrics don't get a series per ID.
func routeOf(r *http.Request) string {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if !strings.HasPrefix(pattern, "/api/") && !quietPaths[pattern] {
		return "web" // the app's own files and client-side routes
	}
	if strings.HasSuffix(pattern, "/*") {
		return "/api/unknown"
	}
	return pattern
}

// clientIP is the address a request came from, without the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // already bare, as the proxy headers give it
	}
	return host
}

// recoverPanics turns a panic in a handler into a logged 500, so one bad
// request can't take the server down or leak a stack trace to the client.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec) // how net/http is told to drop the connection
			}
			s.log.Error("panic",
				"method", r.Method,
				"path", r.URL.Path,
				"request_id", middleware.GetReqID(r.Context()),
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
			)
			writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		}()
		next.ServeHTTP(w, r)
	})
}

// ready reports whether the server can do its job, which comes down to
// reaching the database. Unlike /healthz it is for deciding whether to send
// traffic here, not whether to restart the process.
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		s.log.Warn("readiness check failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "The database is unreachable.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveMetrics writes the metrics for a Prometheus scraper. It exists only
// when a token is configured, and only for requests that present it.
func (s *Server) serveMetrics(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.cfg.MetricsToken == "" || !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.MetricsToken)) != 1 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.registry.Write(w)
}
