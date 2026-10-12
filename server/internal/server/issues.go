package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	maxIssueTitleLen       = 500
	maxIssueDescriptionLen = 100_000 // bytes
	maxIssuePriority       = 4
	maxIssueLabels         = 50
)

// errNoChange rolls back a transaction that turned out to have nothing to do.
var errNoChange = errors.New("no change")

func (s *Server) CreateIssue(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	access, ok := s.requireTeamAccess(w, r, teamId)
	if !ok {
		return
	}
	var req api.CreateIssueRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Issue ID is required.")
		return
	}
	title, apiErr := cleanIssueTitle(req.Title)
	if apiErr == nil {
		apiErr = validateIssueFields(req.Description, req.Priority)
	}
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	var labelIDs []uuid.UUID
	if req.LabelIds != nil {
		labelIDs = uniqueIDs(*req.LabelIds)
	}
	ctx := r.Context()
	team := access.team
	creatorID := currentSession(r).User.ID

	// existing answers a retry of a create that already went through.
	existing := func(q *dbgen.Queries) (*api.IssueMutationResult, error) {
		issue, err := q.GetIssue(ctx, req.Id)
		if db.IsNoRows(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if issue.TeamID != team.ID {
			return nil, &apiError{http.StatusConflict, "id_taken", "That issue ID is already in use."}
		}
		// Whatever created it is in the log at or before the latest sync ID.
		syncID, err := q.GetLastSyncID(ctx, team.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return &api.IssueMutationResult{Issue: issueRecord(issue), SyncId: syncID}, nil
	}

	var (
		result  api.IssueMutationResult
		created bool
	)
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if found, err := existing(q); err != nil {
			return err
		} else if found != nil {
			result = *found
			return nil
		}

		var state dbgen.WorkflowState
		var err error
		switch {
		case team.TriageEnabled && !access.isMember:
			// Issues from outside the team wait in triage until it accepts them.
			if state, err = q.GetTriageState(ctx, team.ID); db.IsNoRows(err) {
				state, err = q.GetDefaultWorkflowState(ctx, team.ID)
			}
		case req.StateId != nil:
			state, err = teamWorkflowState(ctx, q, team.ID, *req.StateId)
		default:
			state, err = q.GetDefaultWorkflowState(ctx, team.ID)
		}
		if err != nil {
			return err
		}
		plan := issuePlan{projectID: req.ProjectId, milestoneID: req.MilestoneId, cycleID: req.CycleId, parentID: req.ParentId}
		if err := checkIssuePlan(ctx, q, team, creatorID, req.Id, plan); err != nil {
			return err
		}
		if err := checkAssignee(ctx, q, team.WorkspaceID, req.AssigneeId); err != nil {
			return err
		}
		if err := checkLabels(ctx, q, team.WorkspaceID, labelIDs); err != nil {
			return err
		}

		number, err := q.NextIssueNumber(ctx, team.ID)
		if err != nil {
			return err
		}
		var sortOrder float64
		if req.SortOrder != nil {
			sortOrder = *req.SortOrder
		} else {
			// New issues go to the top of the list.
			top, err := q.MinIssueSortOrder(ctx, team.ID)
			if err != nil {
				return err
			}
			sortOrder = top - 1
		}
		// The creator, the assignee and anyone mentioned follow the issue.
		var description string
		if req.Description != nil {
			description = *req.Description
		}
		mentions, err := visibleTo(ctx, q, team, mentionedUsers(description))
		if err != nil {
			return err
		}
		subscribers := append([]uuid.UUID{creatorID}, mentions...)
		if req.AssigneeId != nil {
			subscribers = append(subscribers, *req.AssigneeId)
		}
		params := dbgen.CreateIssueParams{
			SubscriberIds: uniqueIDs(subscribers),
			ProjectID:     req.ProjectId,
			MilestoneID:   req.MilestoneId,
			CycleID:       req.CycleId,
			ParentID:      req.ParentId,
			ID:            req.Id,
			WorkspaceID:   team.WorkspaceID,
			TeamID:        team.ID,
			Number:        number,
			Title:         title,
			StateID:       state.ID,
			AssigneeID:    req.AssigneeId,
			CreatorID:     &creatorID,
			SortOrder:     sortOrder,
			LabelIds:      labelIDs,
		}
		if params.LabelIds == nil {
			params.LabelIds = []uuid.UUID{}
		}
		if req.DueDate != nil {
			params.DueDate = &req.DueDate.Time
		}
		params.Description = description
		if req.Priority != nil {
			params.Priority = int16(*req.Priority)
		}
		issue, err := q.CreateIssue(ctx, params)
		if err != nil {
			return err
		}
		result.Issue = issueRecord(issue)
		if result.SyncId, err = recordIssue(ctx, q, team, issue, syncer.Insert); err != nil {
			return err
		}
		created = true

		if err := logActivities(ctx, q, team, issue, &creatorID, []activity{{kind: api.ActivityTypeCreated}}); err != nil {
			return err
		}
		if issue.AssigneeID != nil {
			if err := notify(ctx, q, team, issue, creatorID, api.NotificationTypeAssigned, nil, []uuid.UUID{*issue.AssigneeID}); err != nil {
				return err
			}
		}
		return notify(ctx, q, team, issue, creatorID, api.NotificationTypeMentioned, nil, mentions)
	})
	if db.IsUniqueViolation(err) {
		// A concurrent request created the same issue first.
		var found *api.IssueMutationResult
		if found, err = existing(s.q); err == nil && found != nil {
			result = *found
		} else if err == nil {
			err = &apiError{http.StatusConflict, "conflict", "The issue could not be created. Try again."}
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if created {
		writeJSON(w, http.StatusCreated, result)
	} else {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) UpdateIssue(w http.ResponseWriter, r *http.Request, issueId uuid.UUID) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	var req api.UpdateIssueRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	update, apiErr := prepareIssueUpdate(issueId, req)
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()
	actorID := currentSession(r).User.ID

	var result api.IssueMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		var err error
		result, err = applyIssueUpdate(ctx, q, team, &actorID, update)
		return err
	})
	if errors.Is(err, errNoChange) {
		err = currentIssue(ctx, s.q, team, issueId, &result)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// maxBatchUpdates caps how many issues one batch may change.
const maxBatchUpdates = 200

// UpdateIssues applies several updates in one transaction, which is how a
// bulk edit of many issues reaches the server as one request.
func (s *Server) UpdateIssues(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateIssuesRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Updates) == 0 || len(req.Updates) > maxBatchUpdates {
		writeError(w, http.StatusBadRequest, "invalid_batch", "A batch must have between 1 and 200 updates.")
		return
	}
	ctx := r.Context()
	actorID := currentSession(r).User.ID

	// Check access and validate everything before changing anything.
	type step struct {
		team   dbgen.Team
		update issueUpdate
	}
	steps := make([]step, 0, len(req.Updates))
	teams := map[uuid.UUID]dbgen.Team{}
	for _, item := range req.Updates {
		issue, err := s.q.GetIssue(ctx, item.IssueId)
		if db.IsNoRows(err) {
			notFound(w)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		team, seen := teams[issue.TeamID]
		if !seen {
			access, ok := s.requireTeamAccess(w, r, issue.TeamID)
			if !ok {
				return
			}
			team = access.team
			teams[team.ID] = team
		}
		update, apiErr := prepareIssueUpdate(item.IssueId, item.Patch)
		if apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		steps = append(steps, step{team: team, update: update})
	}

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		for _, st := range steps {
			result, err := applyIssueUpdate(ctx, q, st.team, &actorID, st.update)
			if errors.Is(err, errNoChange) {
				continue
			}
			if err != nil {
				return err
			}
			ack.SyncId = result.SyncId
		}
		if ack.SyncId == 0 {
			// Nothing changed. Every step's team is in one workspace or the
			// caller couldn't have passed the checks; report where the log is.
			var err error
			ack.SyncId, err = q.GetLastSyncID(ctx, steps[0].team.WorkspaceID)
			return err
		}
		return nil
	})
	s.respond(w, r, err, ack)
}

// issueUpdate is a validated request to change an issue.
type issueUpdate struct {
	params                  dbgen.UpdateIssueParams
	addLabels, removeLabels []uuid.UUID
	// changes is false when the request asks for nothing at all.
	changes bool
	// touch marks the issue as looked at when nothing else is changing.
	touch bool
}

// prepareIssueUpdate validates an update request and turns it into the
// parameters of the update. It doesn't touch the database.
func prepareIssueUpdate(issueID uuid.UUID, req api.UpdateIssueRequest) (issueUpdate, *apiError) {
	u := issueUpdate{params: dbgen.UpdateIssueParams{
		ID:          issueID,
		Description: req.Description,
		StateID:     req.StateId,
		SortOrder:   req.SortOrder,
	}}
	params := &u.params
	if req.Title != nil {
		title, apiErr := cleanIssueTitle(*req.Title)
		if apiErr != nil {
			return u, apiErr
		}
		params.Title = &title
	}
	if apiErr := validateIssueFields(req.Description, req.Priority); apiErr != nil {
		return u, apiErr
	}
	if req.Priority != nil {
		priority := int16(*req.Priority)
		params.Priority = &priority
	}
	// The assignee is tri-state: absent leaves it, null clears it.
	if req.AssigneeId.IsSpecified() {
		params.SetAssignee = true
		if !req.AssigneeId.IsNull() {
			id := req.AssigneeId.MustGet()
			params.AssigneeID = &id
		}
	}
	// Due date is tri-state in the same way.
	if req.DueDate.IsSpecified() {
		params.SetDueDate = true
		if !req.DueDate.IsNull() {
			date := req.DueDate.MustGet().Time
			params.DueDate = &date
		}
	}
	params.SetProject, params.ProjectID = optionalID(req.ProjectId)
	params.SetMilestone, params.MilestoneID = optionalID(req.MilestoneId)
	params.SetCycle, params.CycleID = optionalID(req.CycleId)
	params.SetParent, params.ParentID = optionalID(req.ParentId)
	if req.AddLabelIds != nil {
		u.addLabels = uniqueIDs(*req.AddLabelIds)
	}
	if req.RemoveLabelIds != nil {
		u.removeLabels = *req.RemoveLabelIds
	}
	u.touch = req.Touch != nil && *req.Touch
	u.changes = params.Title != nil || params.Description != nil || params.StateID != nil ||
		params.Priority != nil || params.SortOrder != nil || params.SetAssignee ||
		params.SetDueDate || len(u.addLabels) > 0 || len(u.removeLabels) > 0 ||
		params.SetProject || params.SetMilestone || params.SetCycle || params.SetParent
	return u, nil
}

// applyIssueUpdate changes an issue inside a transaction, with everything
// that follows from the change: the team's automation, the sync log, the
// activity feed, subscriptions and notifications. actor is who is making
// the change, or nil when the system is (a pull request merged, a parent
// closing after its sub-issues). It returns errNoChange if the issue already
// is as requested.
func applyIssueUpdate(ctx context.Context, q *dbgen.Queries, team dbgen.Team, actor *uuid.UUID, u issueUpdate) (api.IssueMutationResult, error) {
	var result api.IssueMutationResult
	params := u.params
	issueID := params.ID
	if !u.changes && u.touch {
		return touchIssue(ctx, q, team, actor, issueID)
	}
	if !u.changes {
		return result, errNoChange
	}
	// The zero ID stands for "the system" where an actor is recorded.
	var actorID uuid.UUID
	if actor != nil {
		actorID = *actor
	}
	var newState *dbgen.WorkflowState
	if params.StateID != nil {
		state, err := teamWorkflowState(ctx, q, team.ID, *params.StateID)
		if err != nil {
			return result, err
		}
		newState = &state
	}
	if err := checkAssignee(ctx, q, team.WorkspaceID, params.AssigneeID); err != nil {
		return result, err
	}
	if err := checkLabels(ctx, q, team.WorkspaceID, u.addLabels); err != nil {
		return result, err
	}
	// Lock the row: labels and subscribers are edited by reading the
	// current set and writing a new one, which must not interleave.
	before, err := q.GetIssueForUpdate(ctx, issueID)
	if err != nil {
		return result, err
	}
	if newState != nil && newState.ID != before.StateID {
		if err := automateStart(ctx, q, team, before, *newState, actor, &params); err != nil {
			return result, err
		}
	}
	if len(u.addLabels) > 0 || len(u.removeLabels) > 0 {
		params.LabelIds = relabelIssue(before.LabelIds, u.addLabels, u.removeLabels)
	}
	// A milestone belongs to a project: moving to another project drops
	// it unless the request names a new one.
	if params.SetProject && !params.SetMilestone && !equalPtr(params.ProjectID, before.ProjectID) {
		params.SetMilestone = true
	}
	plan := issuePlan{projectID: before.ProjectID, milestoneID: before.MilestoneID}
	if params.SetProject {
		plan.projectID = params.ProjectID
	}
	if params.SetMilestone {
		plan.milestoneID = params.MilestoneID
	}
	if u.params.SetCycle {
		// Only a cycle the request asked for needs checking; one that
		// automation picked is the team's own.
		plan.cycleID = params.CycleID
	}
	if params.SetParent {
		plan.parentID = params.ParentID
	}
	if err := checkIssuePlan(ctx, q, team, actorID, issueID, plan); err != nil {
		return result, err
	}

	// A new assignee, and anyone newly mentioned, starts following the issue.
	var mentions []uuid.UUID
	if params.Description != nil {
		newly := without(mentionedUsers(*params.Description), mentionedUsers(before.Description))
		if mentions, err = visibleTo(ctx, q, team, newly); err != nil {
			return result, err
		}
	}
	subscribers := append(slices.Clone(before.SubscriberIds), mentions...)
	if params.AssigneeID != nil {
		subscribers = append(subscribers, *params.AssigneeID)
	}
	if subscribers = uniqueIDs(subscribers); len(subscribers) != len(before.SubscriberIds) {
		params.SubscriberIds = subscribers
	}

	issue, err := q.UpdateIssue(ctx, params)
	if err != nil {
		return result, err
	}
	entries := issueChanges(before, issue)
	if len(entries) == 0 && issue.SortOrder == before.SortOrder {
		// Everything was already as requested; undo the timestamp bump.
		return result, errNoChange
	}
	result.Issue = issueRecord(issue)
	if result.SyncId, err = recordIssue(ctx, q, team, issue, syncer.Update); err != nil {
		return result, err
	}
	if err := logActivities(ctx, q, team, issue, actor, entries); err != nil {
		return result, err
	}

	if issue.AssigneeID != nil && !equalPtr(before.AssigneeID, issue.AssigneeID) {
		if err := notify(ctx, q, team, issue, actorID, api.NotificationTypeAssigned, nil, []uuid.UUID{*issue.AssigneeID}); err != nil {
			return result, err
		}
	}
	if issue.StateID != before.StateID {
		if err := notify(ctx, q, team, issue, actorID, api.NotificationTypeStatus, nil, before.SubscriberIds); err != nil {
			return result, err
		}
	}
	if err := notify(ctx, q, team, issue, actorID, api.NotificationTypeMentioned, nil, mentions); err != nil {
		return result, err
	}
	if newState != nil && issue.StateID != before.StateID {
		oldState, err := q.GetWorkflowState(ctx, before.StateID)
		if err != nil {
			return result, err
		}
		completed := string(api.WorkflowStateTypeCompleted)
		if err := followIssueToRequests(ctx, q, issue.ID, oldState.Type == completed, newState.Type == completed); err != nil {
			return result, err
		}
	}
	if newState != nil && issue.StateID != before.StateID && isClosedType(newState.Type) && issue.ParentID != nil {
		if err := closeParentIfDone(ctx, q, *issue.ParentID); err != nil {
			return result, err
		}
	}
	return result, nil
}

// touchIssue records that someone looked at an issue and wants it kept, so
// it stops counting as stale. Nothing about the issue itself changes.
func touchIssue(ctx context.Context, q *dbgen.Queries, team dbgen.Team, actor *uuid.UUID, issueID uuid.UUID) (api.IssueMutationResult, error) {
	var result api.IssueMutationResult
	issue, err := q.TouchIssue(ctx, issueID)
	if err != nil {
		return result, err
	}
	result.Issue = issueRecord(issue)
	if result.SyncId, err = recordIssue(ctx, q, team, issue, syncer.Update); err != nil {
		return result, err
	}
	return result, logActivities(ctx, q, team, issue, actor, []activity{{kind: api.ActivityTypeReviewed}})
}

func isClosedType(stateType string) bool {
	return stateType == string(api.WorkflowStateTypeCompleted) || stateType == string(api.WorkflowStateTypeCanceled)
}

// automateStart adds to an update what the team has asked to happen when
// work on an issue begins: whoever starts an unassigned issue takes it, and
// it joins the current cycle. Anything the update sets itself is left alone.
func automateStart(ctx context.Context, q *dbgen.Queries, team dbgen.Team, before dbgen.Issue, to dbgen.WorkflowState, actor *uuid.UUID, params *dbgen.UpdateIssueParams) error {
	if to.Type != string(api.WorkflowStateTypeStarted) {
		return nil
	}
	from, err := q.GetWorkflowState(ctx, before.StateID)
	if err != nil {
		return err
	}
	if from.Type == string(api.WorkflowStateTypeStarted) {
		return nil // moving between started states isn't starting
	}
	if team.AutoAssign && actor != nil && before.AssigneeID == nil && !params.SetAssignee {
		params.SetAssignee, params.AssigneeID = true, actor
	}
	if team.AutoCycle && team.CycleWeeks > 0 && before.CycleID == nil && !params.SetCycle {
		cycle, err := q.GetCurrentCycle(ctx, dbgen.GetCurrentCycleParams{TeamID: team.ID, Day: dateOf(time.Now())})
		if err == nil {
			params.SetCycle, params.CycleID = true, &cycle.ID
		} else if !db.IsNoRows(err) {
			return err
		}
	}
	return nil
}

// closeParentIfDone completes a parent issue once every one of its
// sub-issues is closed and at least one was completed, if the parent's team
// has asked for that. The parent's own parent follows in the same way.
func closeParentIfDone(ctx context.Context, q *dbgen.Queries, parentID uuid.UUID) error {
	parent, err := q.GetIssue(ctx, parentID)
	if err != nil {
		return err
	}
	team, err := q.GetTeam(ctx, parent.TeamID)
	if err != nil {
		return err
	}
	if !team.AutoCloseParent || parent.ArchivedAt != nil || parent.CompletedAt != nil {
		return nil
	}
	types, err := q.ListSubIssueStateTypes(ctx, &parent.ID)
	if err != nil {
		return err
	}
	completed := false
	for _, kind := range types {
		if !isClosedType(kind) {
			return nil
		}
		completed = completed || kind == string(api.WorkflowStateTypeCompleted)
	}
	if !completed {
		return nil // everything under it was canceled: that is a decision for a person
	}
	done, ok, err := firstStateOfType(ctx, q, team.ID, api.WorkflowStateTypeCompleted)
	if err != nil || !ok {
		return err
	}
	_, err = applyIssueUpdate(ctx, q, team, nil, issueUpdate{params: dbgen.UpdateIssueParams{ID: parent.ID, StateID: &done.ID}, changes: true})
	if errors.Is(err, errNoChange) {
		return nil
	}
	return err
}

func (s *Server) ArchiveIssue(w http.ResponseWriter, r *http.Request, issueId uuid.UUID) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	ctx := r.Context()

	var result api.IssueMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		before, err := q.GetIssue(ctx, issueId)
		if err != nil {
			return err
		}
		if before.ArchivedAt != nil {
			return currentIssue(ctx, q, team, issueId, &result)
		}
		issue, err := q.ArchiveIssue(ctx, issueId)
		if err != nil {
			return err
		}
		result.Issue = issueRecord(issue)
		if result.SyncId, err = recordIssue(ctx, q, team, issue, syncer.Update); err != nil {
			return err
		}
		actorID := currentSession(r).User.ID
		return logActivities(ctx, q, team, issue, &actorID, []activity{{kind: api.ActivityTypeArchived}})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// requireIssueAccess loads the team of an issue the caller is allowed to see.
func (s *Server) requireIssueAccess(w http.ResponseWriter, r *http.Request, issueID uuid.UUID) (dbgen.Team, bool) {
	issue, err := s.q.GetIssue(r.Context(), issueID)
	if db.IsNoRows(err) {
		notFound(w)
		return dbgen.Team{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return dbgen.Team{}, false
	}
	access, ok := s.requireTeamAccess(w, r, issue.TeamID)
	return access.team, ok
}

// currentIssue fills result for a request that changed nothing.
func currentIssue(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issueID uuid.UUID, result *api.IssueMutationResult) error {
	issue, err := q.GetIssue(ctx, issueID)
	if err != nil {
		return err
	}
	result.Issue = issueRecord(issue)
	result.SyncId, err = q.GetLastSyncID(ctx, team.WorkspaceID)
	return err
}

func recordIssue(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issue dbgen.Issue, action string) (int64, error) {
	return syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: team.WorkspaceID,
		Model:       api.SyncModelIssue,
		ModelID:     issue.ID,
		Action:      action,
		Data:        issueRecord(issue),
		Team:        &team,
	})
}

// teamWorkflowState loads a workflow state, requiring it to belong to the team.
func teamWorkflowState(ctx context.Context, q *dbgen.Queries, teamID, stateID uuid.UUID) (dbgen.WorkflowState, error) {
	state, err := q.GetWorkflowState(ctx, stateID)
	if db.IsNoRows(err) || (err == nil && state.TeamID != teamID) {
		return state, badRequest("invalid_state", "That status doesn't belong to this team.")
	}
	return state, err
}

// checkAssignee requires the assignee, if any, to be a member of the workspace.
func checkAssignee(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, assigneeID *uuid.UUID) error {
	if assigneeID == nil {
		return nil
	}
	_, err := q.GetWorkspaceRole(ctx, dbgen.GetWorkspaceRoleParams{WorkspaceID: workspaceID, UserID: *assigneeID})
	if db.IsNoRows(err) {
		return badRequest("invalid_assignee", "The assignee is not a member of this workspace.")
	}
	return err
}

func cleanIssueTitle(raw string) (string, *apiError) {
	title := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(title); n < 1 || n > maxIssueTitleLen {
		return "", badRequest("invalid_title", "Issue title must be 1-500 characters.")
	}
	return title, nil
}

func validateIssueFields(description *string, priority *int32) *apiError {
	if description != nil && len(*description) > maxIssueDescriptionLen {
		return badRequest("invalid_description", "Issue description is too long.")
	}
	if priority != nil && (*priority < 0 || *priority > maxIssuePriority) {
		return badRequest("invalid_priority", "Priority must be between 0 and 4.")
	}
	return nil
}

// checkLabels requires every label to exist in the workspace. ids must not
// contain duplicates.
func checkLabels(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxIssueLabels {
		return badRequest("invalid_labels", "Too many labels.")
	}
	found, err := q.CountLabelsIn(ctx, dbgen.CountLabelsInParams{WorkspaceID: workspaceID, Ids: ids})
	if err != nil {
		return err
	}
	if int(found) != len(ids) {
		return badRequest("invalid_labels", "One of those labels doesn't exist in this workspace.")
	}
	return nil
}

// relabelIssue returns current with add appended and remove taken out,
// keeping the existing order and never returning nil.
func relabelIssue(current, add, remove []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(current)+len(add))
	for _, id := range current {
		if !slices.Contains(remove, id) {
			out = append(out, id)
		}
	}
	for _, id := range add {
		if !slices.Contains(out, id) && !slices.Contains(remove, id) {
			out = append(out, id)
		}
	}
	return out
}

// uniqueIDs returns ids without duplicates, in their original order.
func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) SubscribeToIssue(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	s.setSubscription(w, r, issueId, true)
}

func (s *Server) UnsubscribeFromIssue(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	s.setSubscription(w, r, issueId, false)
}

func (s *Server) setSubscription(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, subscribed bool) {
	team, ok := s.requireIssueAccess(w, r, issueID)
	if !ok {
		return
	}
	ctx := r.Context()
	userID := currentSession(r).User.ID

	var result api.IssueMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		before, err := q.GetIssueForUpdate(ctx, issueID)
		if err != nil {
			return err
		}
		if slices.Contains(before.SubscriberIds, userID) == subscribed {
			return errNoChange
		}
		subscribers := without(before.SubscriberIds, []uuid.UUID{userID})
		if subscribed {
			subscribers = append(subscribers, userID)
		}
		if subscribers == nil {
			subscribers = []uuid.UUID{}
		}
		issue, err := q.SetIssueSubscribers(ctx, dbgen.SetIssueSubscribersParams{ID: issueID, SubscriberIds: subscribers})
		if err != nil {
			return err
		}
		result.Issue = issueRecord(issue)
		result.SyncId, err = recordIssue(ctx, q, team, issue, syncer.Update)
		return err
	})
	if errors.Is(err, errNoChange) {
		err = currentIssue(ctx, s.q, team, issueID, &result)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// issuePlan is where an issue sits in the team's planning: its project,
// milestone, cycle and parent issue. Nil fields are not being set.
type issuePlan struct {
	projectID, milestoneID, cycleID, parentID *uuid.UUID
}

// checkIssuePlan validates the planning fields an issue is about to get.
func checkIssuePlan(ctx context.Context, q *dbgen.Queries, team dbgen.Team, userID, issueID uuid.UUID, plan issuePlan) error {
	if plan.projectID != nil {
		project, err := q.GetProject(ctx, *plan.projectID)
		if db.IsNoRows(err) || (err == nil && project.WorkspaceID != team.WorkspaceID) {
			return badRequest("invalid_project", "That project doesn't exist in this workspace.")
		}
		if err != nil {
			return err
		}
	}
	if plan.milestoneID != nil {
		milestone, err := q.GetMilestone(ctx, *plan.milestoneID)
		if db.IsNoRows(err) || (err == nil && (plan.projectID == nil || milestone.ProjectID != *plan.projectID)) {
			return badRequest("invalid_milestone", "That milestone doesn't belong to the issue's project.")
		}
		if err != nil {
			return err
		}
	}
	if plan.cycleID != nil {
		cycle, err := q.GetCycle(ctx, *plan.cycleID)
		if db.IsNoRows(err) || (err == nil && cycle.TeamID != team.ID) {
			return badRequest("invalid_cycle", "That cycle doesn't belong to the issue's team.")
		}
		if err != nil {
			return err
		}
	}
	if plan.parentID != nil {
		if *plan.parentID == issueID || !canSeeIssue(ctx, q, userID, *plan.parentID, team.WorkspaceID) {
			return badRequest("invalid_parent", "That issue can't be the parent.")
		}
		// Walk up from the parent: meeting this issue would make a loop.
		ancestor := plan.parentID
		for depth := 0; ancestor != nil; depth++ {
			if *ancestor == issueID || depth > 50 {
				return badRequest("invalid_parent", "That would make the issue its own ancestor.")
			}
			next, err := q.GetIssueParent(ctx, *ancestor)
			if err != nil {
				return err
			}
			ancestor = next
		}
	}
	return nil
}
