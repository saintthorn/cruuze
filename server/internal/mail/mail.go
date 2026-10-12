// Package mail sends transactional email.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Message struct {
	To      string
	Subject string
	Text    string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// LogSender writes messages to the log instead of delivering them. It is the
// fallback when no email service is configured, so local sign-in works
// without a mailbox.
type LogSender struct{ Log *slog.Logger }

func (s LogSender) Send(_ context.Context, m Message) error {
	s.Log.Info("email (no email service configured, not delivered)", "to", m.To, "subject", m.Subject, "body", m.Text)
	return nil
}

type SMTPSender struct {
	Host, Port string
	User, Pass string
	From       string
}

func (s SMTPSender) Send(_ context.Context, m Message) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("parse MAIL_FROM: %w", err)
	}
	// Reject header injection through the recipient or subject.
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("invalid message headers")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.Text, "\n", "\r\n"))

	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Pass, s.Host)
	}
	return smtp.SendMail(net.JoinHostPort(s.Host, s.Port), auth, from.Address, []string{m.To}, []byte(b.String()))
}

// ResendSender delivers through Resend's HTTP API, for hosts that block
// outbound SMTP.
type ResendSender struct {
	APIKey string
	From   string
	// Client is used for requests; nil means http.DefaultClient.
	Client *http.Client
}

const resendURL = "https://api.resend.com/emails"

func (s ResendSender) Send(ctx context.Context, m Message) error {
	body, err := json.Marshal(map[string]any{
		"from":    s.From,
		"to":      []string{m.To},
		"subject": m.Subject,
		"text":    m.Text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("resend: status %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// DirSender writes each message to a file in Dir instead of delivering it.
// It lets end-to-end tests (and people without a mail server) read what the
// app would have sent, sign-in links included, so never use it in production.
type DirSender struct{ Dir string }

// unsafeInFilename is everything but the characters an address needs.
var unsafeInFilename = regexp.MustCompile(`[^a-zA-Z0-9@._+-]`)

func (s DirSender) Send(_ context.Context, m Message) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	// Named so that a recipient's messages sort oldest to newest.
	name := fmt.Sprintf("%d-%s.txt", time.Now().UnixNano(), unsafeInFilename.ReplaceAllString(m.To, "_"))
	body := fmt.Sprintf("To: %s\nSubject: %s\n\n%s", m.To, m.Subject, m.Text)
	return os.WriteFile(filepath.Join(s.Dir, name), []byte(body), 0o600)
}
