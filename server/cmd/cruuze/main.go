// Command cruuze runs the Cruuze API server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"cruuze/server/internal/blob"
	"cruuze/server/internal/config"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/mail"
	"cruuze/server/internal/server"
	"cruuze/server/internal/webui"
)

func main() {
	// `cruuze healthcheck` is for container health checks, where the image
	// has no shell or curl to make the request with.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// JSON for log collectors; text is easier on the eyes in a terminal.
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, nil)
	if os.Getenv("LOG_FORMAT") == "json" {
		handler = slog.NewJSONHandler(os.Stderr, nil)
	}
	log := slog.New(handler)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// healthcheck asks the server running in this container whether it is ready.
func healthcheck() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/readyz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("not ready: status %d", resp.StatusCode)
	}
	return nil
}

func run(log *slog.Logger) error {
	// Local development convenience; real deployments set the environment
	// directly. One at a time: Load gives up at the first file that is missing.
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var sender mail.Sender = mail.LogSender{Log: log}
	switch {
	case cfg.MailDir != "":
		log.Warn("writing email to files instead of sending it", "dir", cfg.MailDir)
		sender = mail.DirSender{Dir: cfg.MailDir}
	case cfg.ResendAPIKey != "":
		sender = mail.ResendSender{APIKey: cfg.ResendAPIKey, From: cfg.MailFrom}
	case cfg.SMTPHost != "":
		sender = mail.SMTPSender{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort,
			User: cfg.SMTPUser, Pass: cfg.SMTPPass,
			From: cfg.MailFrom,
		}
	}

	go pruneExpired(ctx, pool, log)

	blobs, err := openBlobStore(ctx, cfg, log)
	if err != nil {
		return err
	}

	srv := server.New(cfg, pool, sender, blobs, log, webui.FS())
	go srv.Hub().Listen(ctx, cfg.DatabaseURL, log)
	go srv.KeepCycles(ctx)
	go srv.DeliverWebhooks(ctx)
	go srv.ArchiveClosedIssues(ctx)
	go srv.SendNotificationEmails(ctx)
	go srv.NotifyShippedRequests(ctx)
	go srv.ResumeImports(ctx)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr, "app_url", cfg.AppURL)
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	srv.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openBlobStore picks where attachments are kept: an S3-compatible bucket if
// one is configured, else a directory on local disk.
func openBlobStore(ctx context.Context, cfg config.Config, log *slog.Logger) (blob.Store, error) {
	if cfg.S3Bucket == "" {
		log.Info("storing attachments on local disk", "dir", cfg.StorageDir)
		return blob.NewDir(cfg.StorageDir)
	}
	store, err := blob.NewS3(blob.S3Config{
		Endpoint:  cfg.S3Endpoint,
		Bucket:    cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		Region:    cfg.S3Region,
		Insecure:  cfg.S3Insecure,
	})
	if err != nil {
		return nil, err
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return nil, fmt.Errorf("attachment bucket %q: %w", cfg.S3Bucket, err)
	}
	log.Info("storing attachments in S3", "endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket)
	return store, nil
}

// pruneExpired periodically deletes expired sessions, login tokens, old sync
// log entries and old webhook deliveries.
func pruneExpired(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	q := dbgen.New(pool)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := q.DeleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
			log.Warn("prune sessions", "err", err)
		}
		if err := q.DeleteExpiredLoginTokens(ctx); err != nil && ctx.Err() == nil {
			log.Warn("prune login tokens", "err", err)
		}
		if err := q.DeleteOldSyncActions(ctx); err != nil && ctx.Err() == nil {
			log.Warn("prune sync log", "err", err)
		}
		if err := q.DeleteOldWebhookDeliveries(ctx); err != nil && ctx.Err() == nil {
			log.Warn("prune webhook deliveries", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
