package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	maxWebhooks      = 20
	maxWebhookURLLen = 2000
	// webhookBatch is how many deliveries are sent at once.
	webhookBatch = 20
	// webhookPollEvery is how often due deliveries are looked for when
	// nothing announces them: retries, and wake-ups the hub missed.
	webhookPollEvery = 30 * time.Second

	deliveryPending   = string(api.WebhookDeliveryStatusPending)
	deliveryDelivered = string(api.WebhookDeliveryStatusDelivered)
	deliveryFailed    = string(api.WebhookDeliveryStatusFailed)
)

// webhookBackoff is how long to wait after each failed attempt before the
// next. A delivery that fails once more after the last wait is given up on.
var webhookBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}

func toWebhook(h dbgen.Webhook) api.Webhook {
	out := api.Webhook{
		Id:        h.ID,
		Label:     h.Label,
		Url:       h.Url,
		Secret:    h.Secret,
		TeamId:    h.TeamID,
		Models:    make([]api.SyncModel, 0, len(h.Models)),
		Enabled:   h.Enabled,
		CreatedAt: h.CreatedAt,
	}
	for _, m := range h.Models {
		out.Models = append(out.Models, api.SyncModel(m))
	}
	return out
}

func (s *Server) ListWebhooks(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	hooks, err := s.q.ListWebhooks(r.Context(), workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	visible, err := s.visibleTeamIDs(r.Context(), r, workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.Webhook, 0, len(hooks))
	for _, h := range hooks {
		// A private team's webhooks belong to the admins in that team.
		if h.TeamID == nil || slices.Contains(visible, *h.TeamID) {
			out = append(out, toWebhook(h))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreateWebhook(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	var req api.CreateWebhookRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	target, apiErr := s.cleanWebhookURL(req.Url)
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	models, apiErr := webhookModels(req.Models)
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	label, apiErr := webhookLabel(req.Label, target)
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()
	if req.TeamId != nil {
		// A team the admin can see, in this workspace. Otherwise a webhook
		// could be used to read a private team from outside it.
		access, ok := s.requireTeamAccess(w, r, *req.TeamId)
		if !ok {
			return
		}
		if access.team.WorkspaceID != workspaceId {
			notFound(w)
			return
		}
	}
	existing, err := s.q.ListWebhooks(ctx, workspaceId)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(existing) >= maxWebhooks {
		writeError(w, http.StatusBadRequest, "too_many_webhooks", "This workspace has too many webhooks. Delete one first.")
		return
	}
	userID := currentSession(r).User.ID
	hook, err := s.q.CreateWebhook(ctx, dbgen.CreateWebhookParams{
		ID:          newID(),
		WorkspaceID: workspaceId,
		Label:       label,
		Url:         target,
		Secret:      "whsec_" + auth.NewToken(),
		TeamID:      req.TeamId,
		Models:      models,
		CreatedBy:   &userID,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toWebhook(hook))
}

func (s *Server) UpdateWebhook(w http.ResponseWriter, r *http.Request, webhookId api.WebhookId) {
	hook, ok := s.requireWebhook(w, r, webhookId)
	if !ok {
		return
	}
	var req api.UpdateWebhookRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID := currentSession(r).User.ID
	params := dbgen.UpdateWebhookParams{ID: hook.ID, Enabled: req.Enabled, UpdatedBy: &userID}
	if req.Url != nil {
		target, apiErr := s.cleanWebhookURL(*req.Url)
		if apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		params.Url = &target
	}
	if req.Label != nil {
		label, apiErr := webhookLabel(req.Label, hook.Url)
		if apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		params.Label = &label
	}
	if req.Models != nil {
		models, apiErr := webhookModels(req.Models)
		if apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		params.Models = models
	}
	updated, err := s.q.UpdateWebhook(r.Context(), params)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWebhook(updated))
}

func (s *Server) DeleteWebhook(w http.ResponseWriter, r *http.Request, webhookId api.WebhookId) {
	hook, ok := s.requireWebhook(w, r, webhookId)
	if !ok {
		return
	}
	if err := s.q.DeleteWebhook(r.Context(), hook.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request, webhookId api.WebhookId) {
	hook, ok := s.requireWebhook(w, r, webhookId)
	if !ok {
		return
	}
	rows, err := s.q.ListWebhookDeliveries(r.Context(), hook.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]api.WebhookDelivery, 0, len(rows))
	for _, d := range rows {
		out = append(out, api.WebhookDelivery{
			Id:             d.ID,
			Model:          api.SyncModel(d.Model),
			Action:         d.Action,
			Status:         api.WebhookDeliveryStatus(d.Status),
			Attempts:       d.Attempts,
			ResponseStatus: d.ResponseStatus,
			Error:          d.Error,
			CreatedAt:      d.CreatedAt,
			FinishedAt:     d.FinishedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// requireWebhook loads a webhook of a workspace the caller administers. One
// for a single team also needs the caller to see that team: its secret and
// address decide who reads the team's changes.
func (s *Server) requireWebhook(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Webhook, bool) {
	hook, err := s.q.GetWebhook(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return hook, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return hook, false
	}
	if !s.requireWorkspaceAdmin(w, r, hook.WorkspaceID) {
		return hook, false
	}
	if hook.TeamID != nil {
		if _, ok := s.requireTeamAccess(w, r, *hook.TeamID); !ok {
			return hook, false
		}
	}
	return hook, true
}

// cleanWebhookURL validates where a webhook may send to. Addresses are
// checked again when connecting, since a host name can resolve to anything.
func (s *Server) cleanWebhookURL(raw string) (string, *apiError) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || len(raw) > maxWebhookURLLen || u.Hostname() == "" || u.User != nil {
		return "", badRequest("invalid_url", "Enter a valid webhook URL.")
	}
	if s.cfg.AllowPrivateWebhooks {
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", badRequest("invalid_url", "The webhook URL must start with http:// or https://.")
		}
		return u.String(), nil
	}
	if u.Scheme != "https" {
		return "", badRequest("invalid_url", "The webhook URL must start with https://.")
	}
	if addr, err := netip.ParseAddr(u.Hostname()); (err == nil && !isPublicAddr(addr)) || strings.EqualFold(u.Hostname(), "localhost") {
		return "", badRequest("invalid_url", "The webhook URL must be reachable from the internet.")
	}
	return u.String(), nil
}

// webhookModels validates the models a webhook asks for; nil means all.
func webhookModels(requested *[]api.SyncModel) ([]string, *apiError) {
	wanted := syncer.WebhookModels
	if requested != nil {
		wanted = *requested
	}
	out := make([]string, 0, len(wanted))
	for _, m := range wanted {
		if !slices.Contains(syncer.WebhookModels, m) {
			return nil, badRequest("invalid_models", fmt.Sprintf("Webhooks can't send %q.", m))
		}
		if !slices.Contains(out, string(m)) {
			out = append(out, string(m))
		}
	}
	if len(out) == 0 {
		return nil, badRequest("invalid_models", "Choose at least one kind of record to send.")
	}
	return out, nil
}

// webhookLabel returns the given label, or the URL's host if there is none.
func webhookLabel(label *string, target string) (string, *apiError) {
	if label == nil || strings.TrimSpace(*label) == "" {
		if u, err := url.Parse(target); err == nil {
			return truncate(u.Host, maxNameLen), nil
		}
		return "Webhook", nil
	}
	name, ok := cleanName(*label)
	if !ok {
		return "", badRequest("invalid_label", fmt.Sprintf("Label must be 1-%d characters.", maxNameLen))
	}
	return name, nil
}

// --- Delivery ----------------------------------------------------------------

// DeliverWebhooks sends queued webhook deliveries until ctx is done. Several
// servers can run it at once: each delivery is claimed by one of them.
func (s *Server) DeliverWebhooks(ctx context.Context) {
	wake, cancel := s.hub.SubscribeAll()
	defer cancel()
	ticker := time.NewTicker(webhookPollEvery)
	defer ticker.Stop()
	for {
		for {
			n, err := s.deliverDueWebhooks(ctx)
			if err != nil && ctx.Err() == nil {
				s.log.Warn("webhook delivery", "err", err)
			}
			if err != nil || n < webhookBatch {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
			// Let a burst of changes land, to send them in one pass.
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
}

// deliverDueWebhooks sends one batch of due deliveries and returns its size.
func (s *Server) deliverDueWebhooks(ctx context.Context) (int, error) {
	due, err := s.q.ClaimWebhookDeliveries(ctx, webhookBatch)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	for _, d := range due {
		wg.Go(func() { s.deliverWebhook(ctx, d) })
	}
	wg.Wait()
	return len(due), nil
}

// deliverWebhook makes one attempt at a delivery and records how it went.
func (s *Server) deliverWebhook(ctx context.Context, d dbgen.ClaimWebhookDeliveriesRow) {
	finish := dbgen.FinishWebhookDeliveryParams{ID: d.ID, Status: deliveryDelivered, NextAttemptAt: time.Now()}

	var err error
	if !d.Enabled {
		finish.Status, finish.Error = deliveryFailed, "The webhook was disabled before this could be sent."
	} else {
		var status int32
		status, err = s.postWebhook(ctx, d)
		if status != 0 {
			finish.ResponseStatus = &status
		}
	}
	outcome := "delivered"
	if err != nil || !d.Enabled {
		outcome = "failed"
	}
	s.metrics.webhooks.Inc(outcome)
	if err != nil {
		finish.Error = truncate(err.Error(), 500)
		if retry := int(d.Attempts) - 1; retry < len(webhookBackoff) {
			finish.Status = deliveryPending
			finish.NextAttemptAt = time.Now().Add(webhookBackoff[retry])
		} else {
			finish.Status = deliveryFailed
		}
	}
	// Not ctx: an attempt that was made should be recorded even during shutdown.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.q.FinishWebhookDelivery(recordCtx, finish); err != nil {
		s.log.Warn("record webhook delivery", "id", d.ID, "err", err)
	}
}

// postWebhook sends a delivery and returns the HTTP status it got, if any.
// Anything but a 2xx answer is an error.
func (s *Server) postWebhook(ctx context.Context, d dbgen.ClaimWebhookDeliveriesRow) (int32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Url, bytes.NewReader(d.Payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Cruuze-Webhook")
	req.Header.Set("Cruuze-Event", d.Model)
	// The same across retries, so receivers can drop duplicates.
	req.Header.Set("Cruuze-Delivery", d.ID.String())
	req.Header.Set("Cruuze-Signature", "sha256="+signWebhook(d.Secret, d.Payload))

	resp, err := s.outbound.Do(req)
	if err != nil {
		if errors.Is(err, errPrivateAddress) {
			return 0, errPrivateAddress
		}
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) //nolint:errcheck // only to reuse the connection
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return int32(resp.StatusCode), fmt.Errorf("the endpoint answered %d", resp.StatusCode)
	}
	return int32(resp.StatusCode), nil
}

// signWebhook returns the hex HMAC-SHA256 of body under the webhook's secret.
func signWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
