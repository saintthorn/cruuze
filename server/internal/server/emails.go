package server

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/mail"
)

const (
	notificationEmailEvery = time.Minute
	// maxEmailedNotifications is how many notifications one email lists.
	maxEmailedNotifications = 25
	emailExcerptLen         = 240
)

// SendNotificationEmails emails people about notifications they haven't read
// within the configured delay, until ctx is done. Someone who is in the app
// reads their inbox there and gets no email.
func (s *Server) SendNotificationEmails(ctx context.Context) {
	if s.cfg.NotificationEmailDelay < 0 {
		return
	}
	ticker := time.NewTicker(notificationEmailEvery)
	defer ticker.Stop()
	for {
		if _, err := s.sendNotificationEmails(ctx, time.Now().Add(-s.cfg.NotificationEmailDelay)); err != nil && ctx.Err() == nil {
			s.log.Warn("notification emails", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sendNotificationEmails emails the unread notifications created before the
// cutoff, one message per person and workspace, and returns how many
// messages it sent. A notification is claimed before sending, so it is
// emailed at most once even if sending fails or several servers run this.
func (s *Server) sendNotificationEmails(ctx context.Context, before time.Time) (int, error) {
	rows, err := s.q.ClaimNotificationsToEmail(ctx, before)
	if err != nil {
		return 0, err
	}
	sent := 0
	// Rows arrive ordered by recipient and workspace.
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].UserID == rows[start].UserID && rows[end].WorkspaceID == rows[start].WorkspaceID {
			end++
		}
		group := rows[start:end]
		start = end
		if !group[0].EmailNotifications {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.mail.Send(sendCtx, notificationEmail(s.cfg.AppURL, group))
		cancel()
		if err != nil {
			s.metrics.emails.Inc("failed")
			s.log.Error("send notification email", "err", err)
			continue
		}
		s.metrics.emails.Inc("sent")
		sent++
	}
	return sent, nil
}

// notificationEmail writes the message for one person's unread notifications
// in one workspace.
func notificationEmail(appURL string, rows []dbgen.ClaimNotificationsToEmailRow) mail.Message {
	first := rows[0]
	workspaceURL := appURL + "/" + first.UrlKey

	subject := fmt.Sprintf("%d new notifications in %s", len(rows), first.WorkspaceName)
	if len(rows) == 1 {
		subject = notificationLine(first) + ": " + first.Title
	}

	var b strings.Builder
	for i, row := range rows {
		if i == maxEmailedNotifications {
			fmt.Fprintf(&b, "…and %d more in your inbox: %s/inbox\n\n", len(rows)-i, workspaceURL)
			break
		}
		fmt.Fprintf(&b, "%s\n%s\n", notificationLine(row), row.Title)
		if excerpt := emailExcerpt(row.CommentBody); excerpt != "" {
			fmt.Fprintf(&b, "> %s\n", excerpt)
		}
		fmt.Fprintf(&b, "%s/issue/%s-%d\n\n", workspaceURL, row.TeamKey, row.Number)
	}
	fmt.Fprintf(&b, "--\nYou get these emails for notifications you haven't read in %s.\nTurn them off: %s/settings/account\n", first.WorkspaceName, workspaceURL)

	return mail.Message{To: first.Email, Subject: truncate(strings.ReplaceAll(subject, "\n", " "), 200), Text: b.String()}
}

// notificationLine says what happened, e.g. "Ada commented on ENG-12".
func notificationLine(row dbgen.ClaimNotificationsToEmailRow) string {
	issue := fmt.Sprintf("%s-%d", row.TeamKey, row.Number)
	actor := row.ActorName
	switch api.NotificationType(row.Type) {
	case api.NotificationTypeAssigned:
		if actor == "" {
			return issue + " was assigned to you"
		}
		return actor + " assigned " + issue + " to you"
	case api.NotificationTypeMentioned:
		if actor == "" {
			return "You were mentioned in " + issue
		}
		return actor + " mentioned you in " + issue
	case api.NotificationTypeCommented:
		if actor == "" {
			return "New comment on " + issue
		}
		return actor + " commented on " + issue
	default: // status
		if actor == "" {
			return "The status of " + issue + " changed"
		}
		return actor + " changed the status of " + issue
	}
}

// Mentions and other links are written [text](target) in Markdown.
var markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// emailExcerpt turns a comment into one short line of plain text.
func emailExcerpt(markdown string) string {
	text := markdownLink.ReplaceAllString(markdown, "$1")
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > emailExcerptLen {
		text = string([]rune(text)[:emailExcerptLen]) + "…"
	}
	return text
}
