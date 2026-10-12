package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	mailer "cruuze/server/internal/mail"
	"cruuze/server/internal/syncer"
)

const (
	maxRequestTitleLen = 200
	maxRequestBodyLen  = 10_000
	// Requests one portal visitor may send to a workspace in a day.
	portalRequestsPerDay = 20
	// How long after a request ships its requester is told, so a status set
	// by mistake can be taken back first.
	shippedNoticeDelay = 5 * time.Minute
	shippedNoticeEvery = time.Minute
)

var errNotFound = &apiError{http.StatusNotFound, "not_found", "Not found."}

var requestStatuses = []api.CustomerRequestStatus{
	api.CustomerRequestStatusNew, api.CustomerRequestStatusReviewing, api.CustomerRequestStatusPlanned,
	api.CustomerRequestStatusShipped, api.CustomerRequestStatusDeclined,
}

func customerRequestRecord(r dbgen.CustomerRequest) api.CustomerRequest {
	return api.CustomerRequest{
		Id:             r.ID,
		RequesterEmail: r.RequesterEmail,
		Title:          r.Title,
		Body:           r.Body,
		Status:         api.CustomerRequestStatus(r.Status),
		Public:         r.Public,
		Response:       r.Response,
		IssueId:        r.IssueID,
		VoteCount:      r.VoteCount,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func portalRequest(r dbgen.CustomerRequest, mine, voted bool) api.PortalRequest {
	return api.PortalRequest{
		Id:        r.ID,
		Title:     r.Title,
		Body:      r.Body,
		Status:    api.CustomerRequestStatus(r.Status),
		Response:  r.Response,
		VoteCount: r.VoteCount,
		Voted:     voted,
		Mine:      mine,
		Public:    r.Public,
		CreatedAt: r.CreatedAt,
	}
}

// recordRequest logs a change to a customer's request for everyone in the
// workspace but guests: requests carry customers' addresses.
func recordRequest(ctx context.Context, q *dbgen.Queries, request dbgen.CustomerRequest, action string) (int64, error) {
	return syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: request.WorkspaceID,
		Model:       api.SyncModelCustomerRequest,
		ModelID:     request.ID,
		Action:      action,
		Data:        customerRequestRecord(request),
		MembersOnly: true,
	})
}

func validateRequestText(title, body string) *apiError {
	if title == "" || len(title) > maxRequestTitleLen {
		return badRequest("invalid_title", fmt.Sprintf("A request needs a title of at most %d characters.", maxRequestTitleLen))
	}
	if len(body) > maxRequestBodyLen {
		return badRequest("invalid_body", "That is too long.")
	}
	return nil
}

// --- The team's side -----------------------------------------------------------

func (s *Server) UpdatePortal(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	var req api.UpdatePortalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Intro != nil {
		intro := strings.TrimSpace(*req.Intro)
		if len(intro) > 2000 {
			writeError(w, http.StatusBadRequest, "invalid_intro", "The introduction can be at most 2,000 characters.")
			return
		}
		req.Intro = &intro
	}
	ws, err := s.q.UpdateWorkspacePortal(r.Context(), dbgen.UpdateWorkspacePortalParams{
		ID:          workspaceId,
		Enabled:     req.Enabled,
		PublicBoard: req.PublicBoard,
		Intro:       req.Intro,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWorkspace(ws, roleAdmin))
}

// checkRequestIssue makes sure an issue a request is linked to is one the
// caller can see, in the request's workspace.
func checkRequestIssue(ctx context.Context, q *dbgen.Queries, userID, workspaceID uuid.UUID, issueID *uuid.UUID) error {
	if issueID != nil && !canSeeIssue(ctx, q, userID, *issueID, workspaceID) {
		return badRequest("invalid_issue", "That issue doesn't exist in this workspace.")
	}
	return nil
}

func (s *Server) CreateCustomerRequest(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireEditor(w, r, workspaceId) {
		return
	}
	var req api.CreateCustomerRequestRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.RequesterEmail)
	if !ok || req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_email", "Enter the customer's email address.")
		return
	}
	params := dbgen.CreateCustomerRequestParams{
		ID:             req.Id,
		WorkspaceID:    workspaceId,
		RequesterEmail: email,
		Title:          strings.TrimSpace(req.Title),
		IssueID:        req.IssueId,
	}
	if req.Body != nil {
		params.Body = strings.TrimSpace(*req.Body)
	}
	if apiErr := validateRequestText(params.Title, params.Body); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()
	userID := currentSession(r).User.ID

	var ack api.SyncAck
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := checkRequestIssue(ctx, q, userID, workspaceId, params.IssueID); err != nil {
			return err
		}
		request, err := q.CreateCustomerRequest(ctx, params)
		if db.IsNoRows(err) {
			created = false
			if request, err = q.GetCustomerRequest(ctx, req.Id); err == nil && request.WorkspaceID != workspaceId {
				return errIDTaken
			}
			if err == nil {
				ack.SyncId, err = q.GetLastSyncID(ctx, workspaceId)
			}
			return err
		}
		if err != nil {
			return err
		}
		ack.SyncId, err = recordRequest(ctx, q, request, syncer.Insert)
		return err
	})
	s.respondCreated(w, r, err, created, ack)
}

func (s *Server) UpdateCustomerRequest(w http.ResponseWriter, r *http.Request, requestId uuid.UUID) {
	request, ok := s.requireCustomerRequest(w, r, requestId)
	if !ok {
		return
	}
	var req api.UpdateCustomerRequestRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateCustomerRequestParams{ID: requestId, Public: req.Public}
	title, body := request.Title, request.Body
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		params.Title = &title
	}
	if req.Body != nil {
		body = strings.TrimSpace(*req.Body)
		params.Body = &body
	}
	if apiErr := validateRequestText(title, body); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	if req.Response != nil {
		response := strings.TrimSpace(*req.Response)
		if len(response) > maxRequestBodyLen {
			writeError(w, http.StatusBadRequest, "invalid_body", "That is too long.")
			return
		}
		params.Response = &response
	}
	if req.Status != nil {
		if !slices.Contains(requestStatuses, *req.Status) {
			writeError(w, http.StatusBadRequest, "invalid_status", "That isn't a request status.")
			return
		}
		status := string(*req.Status)
		params.Status = &status
	}
	params.SetIssue, params.IssueID = optionalID(req.IssueId)
	ctx := r.Context()
	userID := currentSession(r).User.ID

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := checkRequestIssue(ctx, q, userID, request.WorkspaceID, params.IssueID); err != nil {
			return err
		}
		updated, err := q.UpdateCustomerRequest(ctx, params)
		if err != nil {
			return err
		}
		ack.SyncId, err = recordRequest(ctx, q, updated, syncer.Update)
		return err
	})
	s.respond(w, r, err, ack)
}

func (s *Server) DeleteCustomerRequest(w http.ResponseWriter, r *http.Request, requestId uuid.UUID) {
	request, ok := s.requireCustomerRequest(w, r, requestId)
	if !ok {
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteCustomerRequest(ctx, requestId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = recordRequest(ctx, q, request, syncer.Delete)
		return err
	})
	s.respond(w, r, err, ack)
}

// requireCustomerRequest loads a request the caller may manage.
func (s *Server) requireCustomerRequest(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.CustomerRequest, bool) {
	request, err := s.q.GetCustomerRequest(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return request, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return request, false
	}
	return request, s.requireEditor(w, r, request.WorkspaceID)
}

// followIssueToRequests keeps customers' requests in step with the issue
// that delivers them: completing it ships them; reopening it before anyone
// has been told takes that back.
func followIssueToRequests(ctx context.Context, q *dbgen.Queries, issueID uuid.UUID, wasCompleted, isCompleted bool) error {
	var changed []dbgen.CustomerRequest
	var err error
	switch {
	case isCompleted && !wasCompleted:
		changed, err = q.ShipRequestsForIssue(ctx, &issueID)
	case wasCompleted && !isCompleted:
		changed, err = q.UnshipRequestsForIssue(ctx, &issueID)
	}
	if err != nil {
		return err
	}
	for _, request := range changed {
		if _, err := recordRequest(ctx, q, request, syncer.Update); err != nil {
			return err
		}
	}
	return nil
}

// --- The customer's side ---------------------------------------------------------

// portalVisitor is who is looking at a portal, if they are signed in. The
// portal is public, so a missing or stale session is not an error.
func (s *Server) portalVisitor(r *http.Request) *dbgen.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	row, err := s.q.GetSessionUser(r.Context(), auth.HashToken(c.Value))
	if err != nil {
		return nil
	}
	return &row.User
}

// requirePortal finds a workspace by its URL key if its portal is open.
func (s *Server) requirePortal(w http.ResponseWriter, r *http.Request, urlKey string) (dbgen.Workspace, bool) {
	ws, err := s.q.GetWorkspaceByURLKey(r.Context(), urlKey)
	if db.IsNoRows(err) || (err == nil && !ws.PortalEnabled) {
		notFound(w)
		return ws, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return ws, false
	}
	return ws, true
}

func (s *Server) GetPortal(w http.ResponseWriter, r *http.Request, urlKey string) {
	ws, ok := s.requirePortal(w, r, urlKey)
	if !ok {
		return
	}
	out := api.Portal{Name: ws.Name, UrlKey: ws.UrlKey, Intro: ws.PortalIntro, PublicBoard: ws.PortalPublicBoard, Requests: []api.PortalRequest{}}
	params := dbgen.ListPortalRequestsParams{WorkspaceID: ws.ID, Board: ws.PortalPublicBoard}
	if visitor := s.portalVisitor(r); visitor != nil {
		out.Email = &visitor.Email
		params.UserID, params.Email = &visitor.ID, &visitor.Email
	}
	if params.UserID != nil || params.Board {
		rows, err := s.q.ListPortalRequests(r.Context(), params)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		for _, row := range rows {
			out.Requests = append(out.Requests, api.PortalRequest{
				Id:        row.ID,
				Title:     row.Title,
				Body:      row.Body,
				Status:    api.CustomerRequestStatus(row.Status),
				Response:  row.Response,
				VoteCount: row.VoteCount,
				Voted:     row.Voted,
				Mine:      row.Mine,
				Public:    row.Public,
				CreatedAt: row.CreatedAt,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) CreatePortalRequest(w http.ResponseWriter, r *http.Request, urlKey string) {
	ws, ok := s.requirePortal(w, r, urlKey)
	if !ok || !s.requireBrowser(w, r) {
		return
	}
	var req api.CreatePortalRequestRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user := currentSession(r).User
	params := dbgen.CreateCustomerRequestParams{
		ID:             req.Id,
		WorkspaceID:    ws.ID,
		RequesterEmail: user.Email,
		RequesterID:    &user.ID,
		Title:          strings.TrimSpace(req.Title),
	}
	if req.Body != nil {
		params.Body = strings.TrimSpace(*req.Body)
	}
	if req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "A request needs an ID.")
		return
	}
	if apiErr := validateRequestText(params.Title, params.Body); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	var request dbgen.CustomerRequest
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		recent, err := q.CountRecentCustomerRequests(ctx, dbgen.CountRecentCustomerRequestsParams{
			WorkspaceID:    ws.ID,
			RequesterEmail: user.Email,
			CreatedAt:      time.Now().Add(-24 * time.Hour),
		})
		if err != nil {
			return err
		}
		request, err = q.CreateCustomerRequest(ctx, params)
		if db.IsNoRows(err) {
			created = false
			request, err = q.GetCustomerRequest(ctx, req.Id)
			if err == nil && (request.WorkspaceID != ws.ID || request.RequesterID == nil || *request.RequesterID != user.ID) {
				return errIDTaken
			}
			return err
		}
		if err != nil {
			return err
		}
		if recent >= portalRequestsPerDay {
			return &apiError{http.StatusTooManyRequests, "rate_limited", "That's a lot of requests for one day. Try again tomorrow."}
		}
		_, err = recordRequest(ctx, q, request, syncer.Insert)
		return err
	})
	s.respondCreated(w, r, err, created, portalRequest(request, true, false))
}

func (s *Server) AddPortalVote(w http.ResponseWriter, r *http.Request, urlKey string, requestId uuid.UUID) {
	s.setPortalVote(w, r, urlKey, requestId, true)
}

func (s *Server) RemovePortalVote(w http.ResponseWriter, r *http.Request, urlKey string, requestId uuid.UUID) {
	s.setPortalVote(w, r, urlKey, requestId, false)
}

func (s *Server) setPortalVote(w http.ResponseWriter, r *http.Request, urlKey string, requestID uuid.UUID, on bool) {
	ws, ok := s.requirePortal(w, r, urlKey)
	if !ok || !s.requireBrowser(w, r) {
		return
	}
	ctx := r.Context()
	user := currentSession(r).User

	var request dbgen.CustomerRequest
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		request, err = q.GetCustomerRequestForUpdate(ctx, requestID)
		// Only what is on the public board can be voted on.
		if db.IsNoRows(err) || (err == nil && (request.WorkspaceID != ws.ID || !request.Public || !ws.PortalPublicBoard)) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		var changed int64
		if on {
			changed, err = q.AddRequestVote(ctx, dbgen.AddRequestVoteParams{RequestID: requestID, UserID: user.ID})
		} else {
			changed, err = q.RemoveRequestVote(ctx, dbgen.RemoveRequestVoteParams{RequestID: requestID, UserID: user.ID})
		}
		if err != nil || changed == 0 {
			return err
		}
		if request, err = q.RecountRequestVotes(ctx, requestID); err != nil {
			return err
		}
		_, err = recordRequest(ctx, q, request, syncer.Update)
		return err
	})
	mine := request.RequesterID != nil && *request.RequesterID == user.ID
	s.respond(w, r, err, portalRequest(request, mine, on))
}

// --- Telling requesters it shipped -------------------------------------------------

// NotifyShippedRequests emails the people who asked for (or voted for)
// something once it has shipped, until ctx is done.
func (s *Server) NotifyShippedRequests(ctx context.Context) {
	ticker := time.NewTicker(shippedNoticeEvery)
	defer ticker.Stop()
	for {
		if _, err := s.notifyShippedRequests(ctx, time.Now().Add(-shippedNoticeDelay)); err != nil && ctx.Err() == nil {
			s.log.Warn("shipped notices", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// notifyShippedRequests sends the notices for requests that shipped before
// the cutoff and returns how many messages it sent. A request is claimed
// before sending, so its people are told at most once.
func (s *Server) notifyShippedRequests(ctx context.Context, before time.Time) (int, error) {
	rows, err := s.q.ClaimShippedRequests(ctx, before)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, row := range rows {
		voters, err := s.q.ListRequestVoterEmails(ctx, row.ID)
		if err != nil {
			return sent, err
		}
		recipients := append([]string{row.RequesterEmail}, voters...)
		slices.Sort(recipients)
		for _, to := range slices.Compact(recipients) {
			sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.mail.Send(sendCtx, shippedEmail(s.cfg.AppURL, to, row))
			cancel()
			if err != nil {
				s.metrics.emails.Inc("failed")
				s.log.Error("send shipped notice", "err", err)
				continue
			}
			s.metrics.emails.Inc("sent")
			sent++
		}
	}
	return sent, nil
}

func shippedEmail(appURL, to string, row dbgen.ClaimShippedRequestsRow) mailer.Message {
	var text strings.Builder
	fmt.Fprintf(&text, "%s has shipped something you asked for:\n\n  %s\n", row.WorkspaceName, row.Title)
	if row.Response != "" {
		fmt.Fprintf(&text, "\n%s\n", row.Response)
	}
	if row.PortalEnabled {
		fmt.Fprintf(&text, "\nYour requests: %s/portal/%s\n", appURL, row.UrlKey)
	}
	return mailer.Message{To: to, Subject: "Shipped: " + truncate(row.Title, 120), Text: text.String()}
}
