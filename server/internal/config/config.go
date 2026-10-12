// Package config loads server configuration from the environment.
package config

import (
	"cruuze/server/internal/seal"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	ListenAddr  string
	// AppURL is the public origin of the web app, without a trailing slash.
	AppURL string

	// Email is written to files in MailDir when that is set (for tests), else
	// goes through Resend's HTTP API when ResendAPIKey is set (for hosts that
	// block SMTP), else through SMTP when SMTPHost is set, else to the log.
	MailDir      string
	ResendAPIKey string
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPass     string
	MailFrom     string

	GoogleClientID     string
	GoogleClientSecret string

	// SecretsKey encrypts the credentials the server keeps for imports: the
	// tokens of connected trackers, and a running import's own. Without it
	// none are kept: no connections, and no carrying on after a restart.
	SecretsKey string
	// OAuth applications registered with each tracker, for connecting to it
	// without a token. Each needs SecretsKey.
	ImportGitHubClientID        string
	ImportGitHubClientSecret    string
	ImportLinearClientID        string
	ImportLinearClientSecret    string
	ImportAtlassianClientID     string
	ImportAtlassianClientSecret string

	// Attachments go to S3-compatible storage when S3Bucket is set, and to
	// StorageDir on local disk otherwise.
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	S3Insecure  bool
	StorageDir  string

	// NotificationEmailDelay is how long a notification must stay unread
	// before it is emailed. Negative turns notification emails off.
	NotificationEmailDelay time.Duration
	// TrustProxy says the server sits behind a reverse proxy that sets
	// X-Real-IP or X-Forwarded-For, which are then believed. Off, those
	// headers are ignored: anyone could forge them to dodge rate limits.
	TrustProxy bool
	// CloudflareAnalytics lets the web app load and report to Cloudflare Web
	// Analytics, whose script Cloudflare adds to pages it proxies.
	CloudflareAnalytics bool
	// MetricsToken is the bearer token that unlocks /metrics. Empty turns
	// the endpoint off.
	MetricsToken string
	// RateLimit is how many API requests one client may make per minute;
	// zero or less means no limit.
	RateLimit int
	// AllowPrivateWebhooks lets webhooks target http:// URLs and private or
	// loopback addresses. For development only: in production it would let
	// admins make the server call services on its internal network.
	AllowPrivateWebhooks bool
	// AllowPrivateImports lets importers read from private and loopback
	// addresses: a Jira Server or GitHub Enterprise inside the network. On a
	// self-hosted install that is usually wanted; on a shared one it would
	// let any workspace admin probe the server's network.
	AllowPrivateImports bool
	// InstanceAdmins are addresses that administer the install, besides
	// the first person who signed in.
	InstanceAdmins []string
}

// Version is the release this binary was built from; set at build time.
var Version = "dev"

func Load() (Config, error) {
	c := Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		ListenAddr:         listenAddr(),
		AppURL:             strings.TrimRight(getenv("APP_URL", "http://localhost:5173"), "/"),
		MailDir:            os.Getenv("MAIL_DIR"),
		ResendAPIKey:       os.Getenv("RESEND_API_KEY"),
		SMTPHost:           os.Getenv("SMTP_HOST"),
		SMTPPort:           getenv("SMTP_PORT", "587"),
		SMTPUser:           os.Getenv("SMTP_USER"),
		SMTPPass:           os.Getenv("SMTP_PASS"),
		MailFrom:           getenv("MAIL_FROM", "Cruuze <noreply@cruuze.local>"),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		SecretsKey:         os.Getenv("SECRETS_KEY"),

		ImportGitHubClientID:        os.Getenv("IMPORT_GITHUB_CLIENT_ID"),
		ImportGitHubClientSecret:    os.Getenv("IMPORT_GITHUB_CLIENT_SECRET"),
		ImportLinearClientID:        os.Getenv("IMPORT_LINEAR_CLIENT_ID"),
		ImportLinearClientSecret:    os.Getenv("IMPORT_LINEAR_CLIENT_SECRET"),
		ImportAtlassianClientID:     os.Getenv("IMPORT_ATLASSIAN_CLIENT_ID"),
		ImportAtlassianClientSecret: os.Getenv("IMPORT_ATLASSIAN_CLIENT_SECRET"),
		S3Endpoint:                  os.Getenv("S3_ENDPOINT"),
		S3Bucket:                    os.Getenv("S3_BUCKET"),
		S3AccessKey:                 os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:                 os.Getenv("S3_SECRET_KEY"),
		S3Region:                    getenv("S3_REGION", "us-east-1"),
		S3Insecure:                  os.Getenv("S3_INSECURE") == "true",
		StorageDir:                  getenv("STORAGE_DIR", "data/attachments"),

		AllowPrivateWebhooks: os.Getenv("WEBHOOK_ALLOW_PRIVATE") == "true",
		AllowPrivateImports:  os.Getenv("IMPORT_ALLOW_PRIVATE") == "true",
		TrustProxy:           os.Getenv("TRUST_PROXY") == "true",
		CloudflareAnalytics:  os.Getenv("CLOUDFLARE_WEB_ANALYTICS") == "true",
		MetricsToken:         os.Getenv("METRICS_TOKEN"),
	}
	for _, email := range strings.Split(os.Getenv("INSTANCE_ADMINS"), ",") {
		if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
			c.InstanceAdmins = append(c.InstanceAdmins, email)
		}
	}
	limit, err := strconv.Atoi(getenv("RATE_LIMIT_PER_MINUTE", "1200"))
	if err != nil {
		return c, fmt.Errorf("RATE_LIMIT_PER_MINUTE must be a number, got %q", os.Getenv("RATE_LIMIT_PER_MINUTE"))
	}
	c.RateLimit = limit
	delay, err := time.ParseDuration(getenv("NOTIFICATION_EMAIL_DELAY", "5m"))
	if err != nil {
		return c, fmt.Errorf("NOTIFICATION_EMAIL_DELAY must be a duration like 5m, got %q", os.Getenv("NOTIFICATION_EMAIL_DELAY"))
	}
	c.NotificationEmailDelay = delay
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	u, err := url.Parse(c.AppURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return c, fmt.Errorf("APP_URL must be an http(s) origin, got %q", c.AppURL)
	}
	if c.S3Bucket != "" && (c.S3Endpoint == "" || c.S3AccessKey == "" || c.S3SecretKey == "") {
		return c, errors.New("S3_BUCKET needs S3_ENDPOINT, S3_ACCESS_KEY and S3_SECRET_KEY")
	}
	if c.SecretsKey != "" {
		if _, err := seal.ParseKey(c.SecretsKey); err != nil {
			return c, fmt.Errorf("SECRETS_KEY %w", err)
		}
	}
	for name, pair := range map[string][2]string{
		"IMPORT_GITHUB":    {c.ImportGitHubClientID, c.ImportGitHubClientSecret},
		"IMPORT_LINEAR":    {c.ImportLinearClientID, c.ImportLinearClientSecret},
		"IMPORT_ATLASSIAN": {c.ImportAtlassianClientID, c.ImportAtlassianClientSecret},
	} {
		if (pair[0] == "") != (pair[1] == "") {
			return c, fmt.Errorf("%s_CLIENT_ID and %s_CLIENT_SECRET go together", name, name)
		}
		if pair[0] != "" && c.SecretsKey == "" {
			return c, fmt.Errorf("%s_CLIENT_ID needs SECRETS_KEY, which encrypts the tokens it brings", name)
		}
	}
	return c, nil
}

// SecureCookies reports whether cookies should carry the Secure attribute.
func (c Config) SecureCookies() bool {
	return strings.HasPrefix(c.AppURL, "https://")
}

func (c Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

// listenAddr is LISTEN_ADDR, else the PORT that platforms like Railway
// assign, else :8080.
func listenAddr() string {
	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		return addr
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
