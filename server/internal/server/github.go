package server

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/auth"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	maxGithubBody = 5 << 20
	// maxLinkedIssues caps how many issues one pull request can link.
	maxLinkedIssues = 20

	prDraft  = string(api.PullRequestStatusDraft)
	prOpen   = string(api.PullRequestStatusOpen)
	prMerged = string(api.PullRequestStatusMerged)
	prClosed = string(api.PullRequestStatusClosed)
)

func (s *Server) githubIntegration(workspaceID uuid.UUID, integration *dbgen.GithubIntegration) api.GithubIntegration {
	out := api.GithubIntegration{WebhookUrl: s.cfg.AppURL + "/api/integrations/github/" + workspaceID.String()}
	if integration != nil {
		out.Enabled = true
		out.Secret = &integration.Secret
	}
	return out
}

func (s *Server) GetGithubIntegration(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	integration, err := s.q.GetGithubIntegration(r.Context(), workspaceId)
	if db.IsNoRows(err) {
		writeJSON(w, http.StatusOK, s.githubIntegration(workspaceId, nil))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.githubIntegration(workspaceId, &integration))
}

func (s *Server) EnableGithubIntegration(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	userID := currentSession(r).User.ID
	integration, err := s.q.UpsertGithubIntegration(r.Context(), dbgen.UpsertGithubIntegrationParams{
		WorkspaceID: workspaceId,
		Secret:      auth.NewToken(),
		CreatedBy:   &userID,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.githubIntegration(workspaceId, &integration))
}

func (s *Server) DisableGithubIntegration(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireWorkspaceAdmin(w, r, workspaceId) {
		return
	}
	if err := s.q.DeleteGithubIntegration(r.Context(), workspaceId); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// githubEvent is the part of GitHub's webhook payloads that is used. One
// struct covers the events handled: each fills in the fields it has.
type githubEvent struct {
	// pull_request events
	Action      string `json:"action"`
	PullRequest struct {
		HTMLURL string  `json:"html_url"`
		Number  int32   `json:"number"`
		Title   string  `json:"title"`
		Body    *string `json:"body"`
		State   string  `json:"state"`
		Draft   bool    `json:"draft"`
		Merged  bool    `json:"merged"`
		Head    struct {
			Ref string `json:"ref"`
		} `json:"head"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
	// create events (a branch or tag was made) and push events
	Ref     string `json:"ref"`
	RefType string `json:"ref_type"`
	Commits []struct {
		Message string `json:"message"`
		Author  struct {
			Username string `json:"username"`
		} `json:"author"`
	} `json:"commits"`
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	// Who did it.
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// ReceiveGithubWebhook takes GitHub's webhook events. There is no session:
// the request proves itself with an HMAC of its body under the secret the
// workspace gave GitHub.
//
// What the events do:
//   - create (a branch): issues named in the branch are started.
//   - pull_request: the pull request is linked to the issues it names;
//     opening it starts them, asking for review moves them to the team's
//     review status, merging the last open one completes them.
//   - push to the default branch: issues that a commit message says it
//     fixes ("fixes ENG-12") are completed.
func (s *Server) ReceiveGithubWebhook(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	ctx := r.Context()
	integration, err := s.q.GetGithubIntegration(ctx, workspaceId)
	if db.IsNoRows(err) {
		writeError(w, http.StatusNotFound, "not_found", "GitHub is not connected to this workspace.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGithubBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Request body is too large.")
		return
	}
	if !validGithubSignature(integration.Secret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "The signature doesn't match. Check the webhook secret.")
		return
	}
	kind := r.Header.Get("X-GitHub-Event")
	if kind != "pull_request" && kind != "create" && kind != "push" {
		// Pings and events we have no use for.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// GitHub sends JSON either as the body or, form-encoded, in "payload".
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		form, err := url.ParseQuery(string(body))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Request body is not a valid form.")
			return
		}
		body = []byte(form.Get("payload"))
	}
	var event githubEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Request body is not valid JSON.")
		return
	}

	var handle func(q *dbgen.Queries) error
	switch kind {
	case "pull_request":
		pr := event.PullRequest
		// The URL is shown as a link, so it must be one that is safe to follow.
		if !strings.HasPrefix(pr.HTMLURL, "https://") || pr.Number <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "The event has no pull request.")
			return
		}
		link := pullRequestLink{
			url:    truncate(pr.HTMLURL, 500),
			repo:   truncate(event.Repository.FullName, 200),
			number: pr.Number,
			title:  truncate(pr.Title, maxIssueTitleLen),
			branch: truncate(pr.Head.Ref, 255),
			author: truncate(pr.User.Login, 100),
			status: prOpen,
			// Asking for review, or taking a pull request out of draft.
			wantsReview: event.Action == "review_requested" || event.Action == "ready_for_review",
		}
		switch {
		case pr.Merged:
			link.status = prMerged
		case pr.State == "closed":
			link.status = prClosed
		case pr.Draft:
			link.status = prDraft
		}
		text := pr.Head.Ref + "\n" + pr.Title
		if pr.Body != nil {
			text += "\n" + *pr.Body
		}
		login := cmp.Or(event.Sender.Login, pr.User.Login)
		handle = func(q *dbgen.Queries) error {
			actor, err := githubActor(ctx, q, workspaceId, login)
			if err != nil {
				return err
			}
			return linkPullRequest(ctx, q, workspaceId, link, issueRefs(text), actor)
		}
	case "create":
		if event.RefType != "branch" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		handle = func(q *dbgen.Queries) error {
			actor, err := githubActor(ctx, q, workspaceId, event.Sender.Login)
			if err != nil {
				return err
			}
			return moveReferenced(ctx, q, workspaceId, issueRefs(event.Ref), api.WorkflowStateTypeStarted, actor)
		}
	case "push":
		// Only what lands on the default branch counts as done.
		if event.Repository.DefaultBranch == "" || event.Ref != "refs/heads/"+event.Repository.DefaultBranch {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		handle = func(q *dbgen.Queries) error {
			for _, commit := range event.Commits {
				refs := closingRefs(commit.Message)
				if len(refs) == 0 {
					continue
				}
				actor, err := githubActor(ctx, q, workspaceId, cmp.Or(commit.Author.Username, event.Sender.Login))
				if err != nil {
					return err
				}
				if err := moveReferenced(ctx, q, workspaceId, refs, api.WorkflowStateTypeCompleted, actor); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if err := db.InTx(ctx, s.pool, handle); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// githubActor finds the workspace member who gave this GitHub username as
// theirs, so that what they did on GitHub is credited to them. Nil if nobody did.
func githubActor(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, login string) (*uuid.UUID, error) {
	if login == "" {
		return nil, nil
	}
	user, err := q.GetMemberByGithubLogin(ctx, dbgen.GetMemberByGithubLoginParams{WorkspaceID: workspaceID, Login: login})
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user.ID, nil
}

// Commit messages close issues the way they close GitHub's own: a keyword,
// then one or more identifiers.
var closingPattern = regexp.MustCompile(`(?i)\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?)[:\s]+((?:[a-z][a-z0-9]{0,6}-[0-9]{1,9})(?:\s*(?:,|and)\s*[a-z][a-z0-9]{0,6}-[0-9]{1,9})*)`)

// closingRefs finds the issues a commit message says it fixes.
func closingRefs(message string) []issueRef {
	var refs []issueRef
	for _, m := range closingPattern.FindAllStringSubmatch(message, -1) {
		for _, ref := range issueRefs(m[1]) {
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// moveReferenced moves the named issues forward to a kind of state: started
// if they haven't begun, completed if they aren't closed. Identifiers that
// aren't issues of this workspace are ignored.
func moveReferenced(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, refs []issueRef, kind api.WorkflowStateType, actor *uuid.UUID) error {
	for _, ref := range refs {
		issue, err := q.GetIssueByTeamKeyAndNumber(ctx, dbgen.GetIssueByTeamKeyAndNumberParams{WorkspaceID: workspaceID, Key: ref.teamKey, Number: ref.number})
		if db.IsNoRows(err) {
			continue
		}
		if err != nil {
			return err
		}
		team, err := q.GetTeam(ctx, issue.TeamID)
		if err != nil {
			return err
		}
		if err := moveForward(ctx, q, team, issue, kind, actor); err != nil {
			return err
		}
	}
	return nil
}

// moveForward takes an issue to the team's first state of a kind, but never
// backwards: only waiting issues are started, only open ones completed.
func moveForward(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issue dbgen.Issue, kind api.WorkflowStateType, actor *uuid.UUID) error {
	if issue.ArchivedAt != nil {
		return nil
	}
	current, err := q.GetWorkflowState(ctx, issue.StateID)
	if err != nil {
		return err
	}
	switch kind {
	case api.WorkflowStateTypeStarted:
		if current.Type != string(api.WorkflowStateTypeBacklog) && current.Type != string(api.WorkflowStateTypeUnstarted) {
			return nil
		}
	case api.WorkflowStateTypeCompleted:
		if isClosedType(current.Type) {
			return nil
		}
	}
	next, ok, err := firstStateOfType(ctx, q, team.ID, kind)
	if err != nil || !ok {
		return err // a team without such a state is left alone
	}
	return moveToState(ctx, q, team, issue.ID, next.ID, actor)
}

// moveToState changes an issue's state the way a person would, so the
// team's automation, the activity feed and notifications all follow.
func moveToState(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issueID, stateID uuid.UUID, actor *uuid.UUID) error {
	_, err := applyIssueUpdate(ctx, q, team, actor, issueUpdate{params: dbgen.UpdateIssueParams{ID: issueID, StateID: &stateID}, changes: true})
	if errors.Is(err, errNoChange) {
		return nil
	}
	return err
}

// validGithubSignature checks GitHub's X-Hub-Signature-256 header.
func validGithubSignature(secret string, body []byte, header string) bool {
	given, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(given, mac.Sum(nil))
}

// issueRef names an issue the way people write it: ENG-12.
type issueRef struct {
	teamKey string
	number  int32
}

// Team keys are 1-7 letters or digits starting with a letter. Branch names
// are usually lowercase, so case is ignored.
var issueRefPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9]{0,6})-([0-9]{1,9})\b`)

// issueRefs finds the issue identifiers mentioned in text.
func issueRefs(text string) []issueRef {
	var refs []issueRef
	for _, m := range issueRefPattern.FindAllStringSubmatch(text, -1) {
		n, err := strconv.ParseInt(m[2], 10, 32)
		if err != nil {
			continue
		}
		ref := issueRef{teamKey: strings.ToUpper(m[1]), number: int32(n)}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
		if len(refs) == maxLinkedIssues {
			break
		}
	}
	return refs
}

// pullRequestLink is the current state of a pull request, as GitHub reported it.
type pullRequestLink struct {
	url, repo, title, branch, author, status string
	number                                   int32
	// wantsReview says this event is the pull request asking for review.
	wantsReview bool
}

// linkPullRequest records a pull request against the issues it mentions, and
// updates it on the issues it was linked to before. An issue stays linked
// even if a later edit of the pull request drops its identifier. actor is
// the workspace member behind the event, if known.
func linkPullRequest(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, link pullRequestLink, refs []issueRef, actor *uuid.UUID) error {
	existing, err := q.ListPullRequestsByURL(ctx, dbgen.ListPullRequestsByURLParams{WorkspaceID: workspaceID, Url: link.url})
	if err != nil {
		return err
	}
	linked := make(map[uuid.UUID]dbgen.PullRequest, len(existing))
	issueIDs := make([]uuid.UUID, 0, len(existing)+len(refs))
	for _, pr := range existing {
		linked[pr.IssueID] = pr
		issueIDs = append(issueIDs, pr.IssueID)
	}
	for _, ref := range refs {
		issue, err := q.GetIssueByTeamKeyAndNumber(ctx, dbgen.GetIssueByTeamKeyAndNumberParams{WorkspaceID: workspaceID, Key: ref.teamKey, Number: ref.number})
		if db.IsNoRows(err) {
			continue // looked like an identifier, but isn't one of ours
		}
		if err != nil {
			return err
		}
		if !slices.Contains(issueIDs, issue.ID) {
			issueIDs = append(issueIDs, issue.ID)
		}
	}
	// Lock issues in a stable order so concurrent events can't deadlock.
	slices.SortFunc(issueIDs, func(a, b uuid.UUID) int { return cmp.Compare(a.String(), b.String()) })

	for _, issueID := range issueIDs {
		issue, err := q.GetIssueForUpdate(ctx, issueID)
		if err != nil {
			return err
		}
		team, err := q.GetTeam(ctx, issue.TeamID)
		if err != nil {
			return err
		}
		before, wasLinked := linked[issueID]
		pr := before
		switch {
		case !wasLinked:
			pr, err = q.CreatePullRequest(ctx, dbgen.CreatePullRequestParams{
				ID:          newID(),
				WorkspaceID: workspaceID,
				IssueID:     issueID,
				Url:         link.url,
				Repo:        link.repo,
				Number:      link.number,
				Title:       link.title,
				Branch:      link.branch,
				Author:      link.author,
				Status:      link.status,
			})
			if err == nil {
				_, err = record(ctx, q, workspaceID, api.SyncModelPullRequest, pr.ID, syncer.Insert, pullRequestRecord(pr), &team, nil)
			}
		case before.Title != link.title || before.Branch != link.branch || before.Status != link.status:
			pr, err = q.UpdatePullRequest(ctx, dbgen.UpdatePullRequestParams{ID: before.ID, Title: link.title, Branch: link.branch, Status: link.status})
			if err == nil {
				_, err = record(ctx, q, workspaceID, api.SyncModelPullRequest, pr.ID, syncer.Update, pullRequestRecord(pr), &team, nil)
			}
		}
		if err != nil {
			return err
		}
		statusChanged := !wasLinked || before.Status != pr.Status
		if err := followPullRequest(ctx, q, team, issue, pr, statusChanged, before.Status, link.wantsReview, actor); err != nil {
			return err
		}
	}
	return nil
}

// followPullRequest moves an issue along with its pull request: work that
// hasn't started is started when a pull request opens, an issue in progress
// goes to the team's review status when review is asked for, and the issue
// is completed when its last open pull request merges.
func followPullRequest(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issue dbgen.Issue, pr dbgen.PullRequest, statusChanged bool, previous string, wantsReview bool, actor *uuid.UUID) error {
	if issue.ArchivedAt != nil {
		return nil
	}
	switch {
	case statusChanged && pr.Status == prMerged:
		others, err := q.CountOtherOpenPullRequests(ctx, dbgen.CountOtherOpenPullRequestsParams{IssueID: issue.ID, ID: pr.ID})
		if err != nil || others > 0 {
			return err
		}
		return moveForward(ctx, q, team, issue, api.WorkflowStateTypeCompleted, actor)
	case statusChanged && (pr.Status == prOpen || pr.Status == prDraft) && (previous == "" || previous == prClosed):
		if err := moveForward(ctx, q, team, issue, api.WorkflowStateTypeStarted, actor); err != nil {
			return err
		}
	}
	if wantsReview && pr.Status == prOpen && team.ReviewStateID != nil {
		// From any in-progress status, the one it may just have been given included.
		current, err := q.GetIssue(ctx, issue.ID)
		if err != nil {
			return err
		}
		state, err := q.GetWorkflowState(ctx, current.StateID)
		if err != nil {
			return err
		}
		if state.Type == string(api.WorkflowStateTypeStarted) && state.ID != *team.ReviewStateID {
			return moveToState(ctx, q, team, issue.ID, *team.ReviewStateID, actor)
		}
	}
	return nil
}
