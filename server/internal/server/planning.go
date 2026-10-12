package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	defaultProjectColor = "#5e6ad2"
	maxViewOptionsBytes = 16 << 10
)

var projectStatuses = []api.ProjectStatus{
	api.ProjectStatusBacklog, api.ProjectStatusPlanned, api.ProjectStatusStarted,
	api.ProjectStatusPaused, api.ProjectStatusCompleted, api.ProjectStatusCanceled,
}

// optionalID reads a tri-state field: whether it was sent, and its value
// (nil if it was sent as null).
func optionalID(n nullable.Nullable[uuid.UUID]) (set bool, value *uuid.UUID) {
	if !n.IsSpecified() {
		return false, nil
	}
	if n.IsNull() {
		return true, nil
	}
	v := n.MustGet()
	return true, &v
}

func optionalDate(n nullable.Nullable[openapi_types.Date]) (set bool, value *time.Time) {
	if !n.IsSpecified() {
		return false, nil
	}
	if n.IsNull() {
		return true, nil
	}
	v := n.MustGet().Time
	return true, &v
}

func datePtr(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}

func apiDate(t *time.Time) *openapi_types.Date {
	if t == nil {
		return nil
	}
	return &openapi_types.Date{Time: *t}
}

// record logs a change to a record that is visible workspace-wide (team nil),
// to one team, or to one user.
func record(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, model api.SyncModel, id uuid.UUID, action string, data any, team *dbgen.Team, userID *uuid.UUID) (int64, error) {
	return syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: workspaceID,
		Model:       model,
		ModelID:     id,
		Action:      action,
		Data:        data,
		Team:        team,
		UserID:      userID,
	})
}

// requireEditor checks the caller may manage shared workspace content
// (labels, projects, templates): any member except guests.
func (s *Server) requireEditor(w http.ResponseWriter, r *http.Request, workspaceID uuid.UUID) bool {
	role, ok := s.requireWorkspaceRole(w, r, workspaceID)
	if !ok {
		return false
	}
	if role == roleGuest {
		forbidden(w)
		return false
	}
	return true
}

// --- Team settings ---------------------------------------------------------

// DeleteTeam removes a team and everything it owns. Admins only, and by
// the team's ID rather than through requireTeamAccess, so that a private
// team nobody is in any more can still be deleted.
func (s *Server) DeleteTeam(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	ctx := r.Context()
	team, err := s.q.GetTeam(ctx, teamId)
	if err != nil {
		if db.IsNoRows(err) {
			notFound(w)
			return
		}
		s.internalError(w, r, err)
		return
	}
	if !s.requireWorkspaceAdmin(w, r, team.WorkspaceID) {
		return
	}
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteTeam(ctx, team.ID); err != nil {
			return err
		}
		// Far too many records to log one by one: everyone loads afresh.
		return syncer.ResetAll(ctx, q, team.WorkspaceID)
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.log.Info("team deleted", "team", team.Key, "workspace", team.WorkspaceID, "by", currentSession(r).User.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) UpdateTeam(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	access, ok := s.requireTeamAccess(w, r, teamId)
	if !ok {
		return
	}
	// A team is run by its members; admins can step in.
	if access.role == roleGuest || (!access.isMember && access.role != roleAdmin) {
		forbidden(w)
		return
	}
	var req api.UpdateTeamRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Whoever makes a team private must be in it, or they would lock
	// themselves (and possibly everyone) out.
	if req.Private != nil && *req.Private && !access.isMember {
		writeError(w, http.StatusForbidden, "forbidden", "Join the team before making it private.")
		return
	}
	params := dbgen.UpdateTeamSettingsParams{ID: teamId, TriageEnabled: req.TriageEnabled, Private: req.Private}
	if req.Name != nil {
		name, ok := cleanName(*req.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_name", "Team name must be 1-80 characters.")
			return
		}
		params.Name = &name
	}
	if req.Kind != nil {
		if _, ok := teamWorkflows[*req.Kind]; !ok {
			writeError(w, http.StatusBadRequest, "invalid_kind", "That isn't a kind of team.")
			return
		}
		// Only which pages the team is offered changes; its statuses stay.
		kind := string(*req.Kind)
		params.Kind = &kind
	}
	params.AutoCloseParent, params.AutoAssign, params.AutoCycle = req.AutoCloseParent, req.AutoAssign, req.AutoCycle
	params.SetReviewState, params.ReviewStateID = optionalID(req.ReviewStateId)
	if params.ReviewStateID != nil {
		// It has to be one of this team's in-progress statuses.
		state, err := s.q.GetWorkflowState(r.Context(), *params.ReviewStateID)
		if db.IsNoRows(err) || (err == nil && (state.TeamID != teamId || state.Type != string(api.WorkflowStateTypeStarted))) {
			writeError(w, http.StatusBadRequest, "invalid_review_state", "The review status must be one of this team's started statuses.")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if req.StaleDays != nil {
		if *req.StaleDays < 0 || *req.StaleDays > 3650 {
			writeError(w, http.StatusBadRequest, "invalid_stale_days", "Issues can count as stale after 1 to 3650 days, or 0 for never.")
			return
		}
		params.StaleDays = req.StaleDays
	}
	if req.AutoCloseDays != nil {
		// Under two weeks would cancel things people simply haven't got to yet.
		if *req.AutoCloseDays != 0 && (*req.AutoCloseDays < 14 || *req.AutoCloseDays > 3650) {
			writeError(w, http.StatusBadRequest, "invalid_auto_close", "Untouched issues can be canceled after 14 to 3650 days, or 0 for never.")
			return
		}
		params.AutoCloseDays = req.AutoCloseDays
	}
	if req.AutoArchiveDays != nil {
		if *req.AutoArchiveDays < 0 || *req.AutoArchiveDays > 3650 {
			writeError(w, http.StatusBadRequest, "invalid_auto_archive", "Closed issues can be archived after 1 to 3650 days, or 0 for never.")
			return
		}
		params.AutoArchiveDays = req.AutoArchiveDays
	}
	if req.CycleWeeks != nil {
		if *req.CycleWeeks < 0 || *req.CycleWeeks > 8 {
			writeError(w, http.StatusBadRequest, "invalid_cycle_weeks", "Cycles can be 1 to 8 weeks long, or 0 to turn them off.")
			return
		}
		weeks := int16(*req.CycleWeeks)
		params.CycleWeeks = &weeks
	}
	ctx := r.Context()

	var result api.TeamMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		team, err := q.UpdateTeamSettings(ctx, params)
		if err != nil {
			return err
		}
		result.Team = teamRecord(team)
		result.SyncId, err = syncer.Record(ctx, q, syncer.Change{
			WorkspaceID: team.WorkspaceID,
			Model:       api.SyncModelTeam,
			ModelID:     team.ID,
			Action:      syncer.Update,
			Data:        result.Team,
			Team:        &team,
			// People outside the team gain or lose everything in it.
			Resets: team.Private != access.team.Private,
		})
		if err != nil {
			return err
		}
		if team.TriageEnabled {
			if err := ensureTriageState(ctx, q, team); err != nil {
				return err
			}
		}
		if team.CycleWeeks > 0 {
			// Don't make the team wait for the hourly upkeep to get its first cycle.
			return keepTeamCycles(ctx, q, team, time.Now())
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ensureTriageState gives a team its Triage state if it doesn't have one.
func ensureTriageState(ctx context.Context, q *dbgen.Queries, team dbgen.Team) error {
	if _, err := q.GetTriageState(ctx, team.ID); !db.IsNoRows(err) {
		return err
	}
	state, err := q.CreateWorkflowState(ctx, dbgen.CreateWorkflowStateParams{
		ID:          newID(),
		WorkspaceID: team.WorkspaceID,
		TeamID:      team.ID,
		Name:        "Triage",
		Type:        string(api.WorkflowStateTypeTriage),
		Color:       "#f2994a",
		Position:    -1,
	})
	if err != nil {
		return err
	}
	_, err = record(ctx, q, team.WorkspaceID, api.SyncModelWorkflowState, state.ID, syncer.Insert, workflowStateRecord(state), &team, nil)
	return err
}

// --- Projects --------------------------------------------------------------

func (s *Server) CreateProject(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireEditor(w, r, workspaceId) {
		return
	}
	var req api.CreateProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok || req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_name", "Project name must be 1-80 characters.")
		return
	}
	params := dbgen.CreateProjectParams{
		ID:           req.Id,
		WorkspaceID:  workspaceId,
		Name:         name,
		Status:       string(api.ProjectStatusBacklog),
		LeadID:       req.LeadId,
		StartDate:    datePtr(req.StartDate),
		TargetDate:   datePtr(req.TargetDate),
		Color:        defaultProjectColor,
		InitiativeID: req.InitiativeId,
	}
	if req.Description != nil {
		params.Description = *req.Description
	}
	if req.Status != nil {
		params.Status = string(*req.Status)
	}
	if req.Color != nil {
		params.Color = strings.ToLower(*req.Color)
	}
	if apiErr := validateProject(params.Description, api.ProjectStatus(params.Status), params.Color); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	var result api.ProjectMutationResult
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := checkAssignee(ctx, q, workspaceId, params.LeadID); err != nil {
			return err
		}
		if err := checkInitiative(ctx, q, workspaceId, params.InitiativeID); err != nil {
			return err
		}
		project, err := q.CreateProject(ctx, params)
		if db.IsNoRows(err) {
			// A retry: the project is already there.
			created = false
			if project, err = q.GetProject(ctx, req.Id); err == nil && project.WorkspaceID != workspaceId {
				return errIDTaken
			}
			if err == nil {
				result.Project = projectRecord(project)
				result.SyncId, err = q.GetLastSyncID(ctx, workspaceId)
			}
			return err
		}
		if err != nil {
			return err
		}
		result.Project = projectRecord(project)
		result.SyncId, err = record(ctx, q, workspaceId, api.SyncModelProject, project.ID, syncer.Insert, result.Project, nil, nil)
		return err
	})
	s.respondCreated(w, r, err, created, result)
}

func (s *Server) UpdateProject(w http.ResponseWriter, r *http.Request, projectId uuid.UUID) {
	project, ok := s.requireProject(w, r, projectId)
	if !ok {
		return
	}
	var req api.UpdateProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateProjectParams{ID: projectId, Description: req.Description}
	if req.Name != nil {
		name, ok := cleanName(*req.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_name", "Project name must be 1-80 characters.")
			return
		}
		params.Name = &name
	}
	status, color, description := api.ProjectStatus(project.Status), project.Color, project.Description
	if req.Status != nil {
		status = *req.Status
		text := string(status)
		params.Status = &text
	}
	if req.Color != nil {
		color = strings.ToLower(*req.Color)
		params.Color = &color
	}
	if req.Description != nil {
		description = *req.Description
	}
	if apiErr := validateProject(description, status, color); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	params.SetLead, params.LeadID = optionalID(req.LeadId)
	params.SetStartDate, params.StartDate = optionalDate(req.StartDate)
	params.SetTargetDate, params.TargetDate = optionalDate(req.TargetDate)
	params.SetInitiative, params.InitiativeID = optionalID(req.InitiativeId)
	ctx := r.Context()

	var result api.ProjectMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := checkAssignee(ctx, q, project.WorkspaceID, params.LeadID); err != nil {
			return err
		}
		if err := checkInitiative(ctx, q, project.WorkspaceID, params.InitiativeID); err != nil {
			return err
		}
		updated, err := q.UpdateProject(ctx, params)
		if err != nil {
			return err
		}
		result.Project = projectRecord(updated)
		result.SyncId, err = record(ctx, q, project.WorkspaceID, api.SyncModelProject, projectId, syncer.Update, result.Project, nil, nil)
		return err
	})
	s.respond(w, r, err, result)
}

func (s *Server) DeleteProject(w http.ResponseWriter, r *http.Request, projectId uuid.UUID) {
	project, ok := s.requireProject(w, r, projectId)
	if !ok {
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		// Issues and milestones aren't logged one by one: clients drop the
		// project from their issues when they see it deleted.
		if err := q.DeleteProject(ctx, projectId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = record(ctx, q, project.WorkspaceID, api.SyncModelProject, projectId, syncer.Delete, projectRecord(project), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

// requireProject loads a project the caller may manage.
func (s *Server) requireProject(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Project, bool) {
	project, err := s.q.GetProject(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return project, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return project, false
	}
	return project, s.requireEditor(w, r, project.WorkspaceID)
}

func validateProject(description string, status api.ProjectStatus, color string) *apiError {
	if len(description) > maxIssueDescriptionLen {
		return badRequest("invalid_description", "Project description is too long.")
	}
	if !slices.Contains(projectStatuses, status) {
		return badRequest("invalid_status", "That isn't a project status.")
	}
	return validateColor(color)
}

// --- Milestones ------------------------------------------------------------

func (s *Server) CreateMilestone(w http.ResponseWriter, r *http.Request, projectId uuid.UUID) {
	project, ok := s.requireProject(w, r, projectId)
	if !ok {
		return
	}
	var req api.CreateMilestoneRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok || req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_name", "Milestone name must be 1-80 characters.")
		return
	}
	ctx := r.Context()

	var result api.MilestoneMutationResult
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		last, err := q.MaxMilestoneSortOrder(ctx, projectId)
		if err != nil {
			return err
		}
		milestone, err := q.CreateMilestone(ctx, dbgen.CreateMilestoneParams{
			ID:          req.Id,
			WorkspaceID: project.WorkspaceID,
			ProjectID:   projectId,
			Name:        name,
			TargetDate:  datePtr(req.TargetDate),
			SortOrder:   last + 1,
		})
		if db.IsNoRows(err) {
			created = false
			if milestone, err = q.GetMilestone(ctx, req.Id); err == nil && milestone.ProjectID != projectId {
				return errIDTaken
			}
			if err == nil {
				result.Milestone = milestoneRecord(milestone)
				result.SyncId, err = q.GetLastSyncID(ctx, project.WorkspaceID)
			}
			return err
		}
		if err != nil {
			return err
		}
		result.Milestone = milestoneRecord(milestone)
		result.SyncId, err = record(ctx, q, project.WorkspaceID, api.SyncModelMilestone, milestone.ID, syncer.Insert, result.Milestone, nil, nil)
		return err
	})
	s.respondCreated(w, r, err, created, result)
}

func (s *Server) UpdateMilestone(w http.ResponseWriter, r *http.Request, milestoneId uuid.UUID) {
	milestone, ok := s.requireMilestone(w, r, milestoneId)
	if !ok {
		return
	}
	var req api.UpdateMilestoneRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateMilestoneParams{ID: milestoneId, SortOrder: req.SortOrder}
	if req.Name != nil {
		name, ok := cleanName(*req.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_name", "Milestone name must be 1-80 characters.")
			return
		}
		params.Name = &name
	}
	params.SetTargetDate, params.TargetDate = optionalDate(req.TargetDate)
	ctx := r.Context()

	var result api.MilestoneMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		updated, err := q.UpdateMilestone(ctx, params)
		if err != nil {
			return err
		}
		result.Milestone = milestoneRecord(updated)
		result.SyncId, err = record(ctx, q, milestone.WorkspaceID, api.SyncModelMilestone, milestoneId, syncer.Update, result.Milestone, nil, nil)
		return err
	})
	s.respond(w, r, err, result)
}

func (s *Server) DeleteMilestone(w http.ResponseWriter, r *http.Request, milestoneId uuid.UUID) {
	milestone, ok := s.requireMilestone(w, r, milestoneId)
	if !ok {
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteMilestone(ctx, milestoneId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = record(ctx, q, milestone.WorkspaceID, api.SyncModelMilestone, milestoneId, syncer.Delete, milestoneRecord(milestone), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

func (s *Server) requireMilestone(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Milestone, bool) {
	milestone, err := s.q.GetMilestone(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return milestone, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return milestone, false
	}
	return milestone, s.requireEditor(w, r, milestone.WorkspaceID)
}

// --- Relations -------------------------------------------------------------

func (s *Server) CreateRelation(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	var req api.CreateRelationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Id == uuid.Nil || req.RelatedIssueId == issueId ||
		!slices.Contains([]api.RelationType{api.RelationTypeBlocks, api.RelationTypeDuplicate, api.RelationTypeRelated}, req.Type) {
		writeError(w, http.StatusBadRequest, "invalid_relation", "An issue can block, duplicate or relate to another issue.")
		return
	}
	ctx := r.Context()
	// The other issue must be one the caller can see, too.
	if !canSeeIssue(ctx, s.q, currentSession(r).User.ID, req.RelatedIssueId, team.WorkspaceID) {
		writeError(w, http.StatusBadRequest, "invalid_relation", "That issue doesn't exist.")
		return
	}

	var result api.RelationMutationResult
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		relation, err := q.CreateIssueRelation(ctx, dbgen.CreateIssueRelationParams{
			ID:             req.Id,
			WorkspaceID:    team.WorkspaceID,
			IssueID:        issueId,
			RelatedIssueID: req.RelatedIssueId,
			Type:           string(req.Type),
		})
		if db.IsNoRows(err) {
			created = false
			if relation, err = q.GetIssueRelation(ctx, req.Id); err == nil && relation.IssueID != issueId {
				return errIDTaken
			}
			if err == nil {
				result.Relation = relationRecord(relation)
				result.SyncId, err = q.GetLastSyncID(ctx, team.WorkspaceID)
			}
			return err
		}
		if err != nil {
			return err
		}
		result.Relation = relationRecord(relation)
		result.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelRelation, relation.ID, syncer.Insert, result.Relation, &team, nil)
		return err
	})
	if db.IsUniqueViolation(err) {
		err = &apiError{http.StatusConflict, "already_related", "Those issues are already related that way."}
	}
	s.respondCreated(w, r, err, created, result)
}

func (s *Server) DeleteRelation(w http.ResponseWriter, r *http.Request, relationId uuid.UUID) {
	relation, err := s.q.GetIssueRelation(r.Context(), relationId)
	if db.IsNoRows(err) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	team, ok := s.requireIssueAccess(w, r, relation.IssueID)
	if !ok {
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteIssueRelation(ctx, relationId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = record(ctx, q, relation.WorkspaceID, api.SyncModelRelation, relationId, syncer.Delete, relationRecord(relation), &team, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

// canSeeIssue reports whether a user may see an issue of the given workspace.
// It reads through q, so it can be used inside a transaction.
func canSeeIssue(ctx context.Context, q *dbgen.Queries, userID, issueID, workspaceID uuid.UUID) bool {
	issue, err := q.GetIssue(ctx, issueID)
	if err != nil || issue.WorkspaceID != workspaceID {
		return false
	}
	team, err := q.GetTeam(ctx, issue.TeamID)
	if err != nil {
		return false
	}
	visible, err := q.ListUsersWhoCanSeeTeam(ctx, dbgen.ListUsersWhoCanSeeTeamParams{TeamID: team.ID, UserIds: []uuid.UUID{userID}})
	return err == nil && len(visible) == 1
}

// --- Templates -------------------------------------------------------------

func (s *Server) CreateTemplate(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	access, ok := s.requireTeamAccess(w, r, teamId)
	if !ok {
		return
	}
	if access.role == roleGuest {
		forbidden(w)
		return
	}
	var req api.CreateTemplateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok || req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_name", "Template name must be 1-80 characters.")
		return
	}
	team := access.team
	params := dbgen.CreateIssueTemplateParams{ID: req.Id, WorkspaceID: team.WorkspaceID, TeamID: teamId, Name: name, LabelIds: []uuid.UUID{}}
	if req.Title != nil {
		params.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		params.Description = *req.Description
	}
	if req.Priority != nil {
		params.Priority = int16(*req.Priority)
	}
	if req.LabelIds != nil {
		params.LabelIds = uniqueIDs(*req.LabelIds)
	}
	if apiErr := validateTemplate(params.Title, params.Description, int32(params.Priority)); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	var result api.TemplateMutationResult
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := checkLabels(ctx, q, team.WorkspaceID, params.LabelIds); err != nil {
			return err
		}
		template, err := q.CreateIssueTemplate(ctx, params)
		if db.IsNoRows(err) {
			created = false
			if template, err = q.GetIssueTemplate(ctx, req.Id); err == nil && template.TeamID != teamId {
				return errIDTaken
			}
			if err == nil {
				result.Template = templateRecord(template)
				result.SyncId, err = q.GetLastSyncID(ctx, team.WorkspaceID)
			}
			return err
		}
		if err != nil {
			return err
		}
		result.Template = templateRecord(template)
		result.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelTemplate, template.ID, syncer.Insert, result.Template, &team, nil)
		return err
	})
	s.respondCreated(w, r, err, created, result)
}

func (s *Server) UpdateTemplate(w http.ResponseWriter, r *http.Request, templateId uuid.UUID) {
	template, team, ok := s.requireTemplate(w, r, templateId)
	if !ok {
		return
	}
	var req api.UpdateTemplateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateIssueTemplateParams{ID: templateId, Description: req.Description}
	title, description, priority := template.Title, template.Description, int32(template.Priority)
	if req.Name != nil {
		name, ok := cleanName(*req.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_name", "Template name must be 1-80 characters.")
			return
		}
		params.Name = &name
	}
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		params.Title = &title
	}
	if req.Description != nil {
		description = *req.Description
	}
	if req.Priority != nil {
		priority = *req.Priority
		p := int16(priority)
		params.Priority = &p
	}
	if req.LabelIds != nil {
		params.LabelIds = uniqueIDs(*req.LabelIds)
	}
	if apiErr := validateTemplate(title, description, priority); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	var result api.TemplateMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := checkLabels(ctx, q, team.WorkspaceID, params.LabelIds); err != nil {
			return err
		}
		updated, err := q.UpdateIssueTemplate(ctx, params)
		if err != nil {
			return err
		}
		result.Template = templateRecord(updated)
		result.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelTemplate, templateId, syncer.Update, result.Template, &team, nil)
		return err
	})
	s.respond(w, r, err, result)
}

func (s *Server) DeleteTemplate(w http.ResponseWriter, r *http.Request, templateId uuid.UUID) {
	template, team, ok := s.requireTemplate(w, r, templateId)
	if !ok {
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteIssueTemplate(ctx, templateId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelTemplate, templateId, syncer.Delete, templateRecord(template), &team, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

// requireTemplate loads a template of a team the caller can see and, not
// being a guest, may manage.
func (s *Server) requireTemplate(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.IssueTemplate, dbgen.Team, bool) {
	template, err := s.q.GetIssueTemplate(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return template, dbgen.Team{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return template, dbgen.Team{}, false
	}
	access, ok := s.requireTeamAccess(w, r, template.TeamID)
	if !ok {
		return template, dbgen.Team{}, false
	}
	if access.role == roleGuest {
		forbidden(w)
		return template, dbgen.Team{}, false
	}
	return template, access.team, true
}

func validateTemplate(title, description string, priority int32) *apiError {
	if len([]rune(title)) > maxIssueTitleLen {
		return badRequest("invalid_title", "Issue title must be at most 500 characters.")
	}
	return validateIssueFields(&description, &priority)
}

// --- Views -----------------------------------------------------------------

func (s *Server) CreateView(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	role, ok := s.requireWorkspaceRole(w, r, workspaceId)
	if !ok {
		return
	}
	var req api.CreateViewRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok || req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_name", "View name must be 1-80 characters.")
		return
	}
	shared := req.Shared != nil && *req.Shared
	if shared && role == roleGuest {
		forbidden(w)
		return
	}
	options, apiErr := encodeViewOptions(req.Options)
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	if req.TeamId != nil {
		// Don't let a view point at a team the caller can't see.
		if access, ok := s.requireTeamAccess(w, r, *req.TeamId); !ok {
			return
		} else if access.team.WorkspaceID != workspaceId {
			notFound(w)
			return
		}
	}
	ctx := r.Context()
	ownerID := currentSession(r).User.ID

	var result api.ViewMutationResult
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		view, err := q.CreateView(ctx, dbgen.CreateViewParams{
			ID: req.Id, WorkspaceID: workspaceId, OwnerID: ownerID, Name: name, Shared: shared, TeamID: req.TeamId, Options: options,
		})
		if db.IsNoRows(err) {
			created = false
			if view, err = q.GetView(ctx, req.Id); err == nil && (view.WorkspaceID != workspaceId || view.OwnerID != ownerID) {
				return errIDTaken
			}
			if err == nil {
				result.View = viewRecord(view)
				result.SyncId, err = q.GetLastSyncID(ctx, workspaceId)
			}
			return err
		}
		if err != nil {
			return err
		}
		result.View = viewRecord(view)
		result.SyncId, err = recordView(ctx, q, view, syncer.Insert)
		return err
	})
	s.respondCreated(w, r, err, created, result)
}

func (s *Server) UpdateView(w http.ResponseWriter, r *http.Request, viewId uuid.UUID) {
	view, role, ok := s.requireView(w, r, viewId)
	if !ok {
		return
	}
	if view.OwnerID != currentSession(r).User.ID {
		forbidden(w)
		return
	}
	var req api.UpdateViewRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateViewParams{ID: viewId, Shared: req.Shared}
	if req.Name != nil {
		name, ok := cleanName(*req.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_name", "View name must be 1-80 characters.")
			return
		}
		params.Name = &name
	}
	if req.Shared != nil && *req.Shared && role == roleGuest {
		forbidden(w)
		return
	}
	if req.Options != nil {
		options, apiErr := encodeViewOptions(*req.Options)
		if apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		params.Options = options
	}
	ctx := r.Context()

	var result api.ViewMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		updated, err := q.UpdateView(ctx, params)
		if err != nil {
			return err
		}
		result.View = viewRecord(updated)
		if updated.Shared == view.Shared {
			result.SyncId, err = recordView(ctx, q, updated, syncer.Update)
			return err
		}
		// Its audience changed: take it away from those who had it, then
		// hand it to those who should now. The owner sees both.
		if _, err := recordView(ctx, q, view, syncer.Delete); err != nil {
			return err
		}
		result.SyncId, err = recordView(ctx, q, updated, syncer.Insert)
		return err
	})
	s.respond(w, r, err, result)
}

func (s *Server) DeleteView(w http.ResponseWriter, r *http.Request, viewId uuid.UUID) {
	view, role, ok := s.requireView(w, r, viewId)
	if !ok {
		return
	}
	if view.OwnerID != currentSession(r).User.ID && role != roleAdmin {
		forbidden(w)
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteView(ctx, viewId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = recordView(ctx, q, view, syncer.Delete)
		return err
	})
	s.respond(w, r, err, ack)
}

// requireView loads a view the caller can see: a shared one, or their own.
func (s *Server) requireView(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.View, string, bool) {
	view, err := s.q.GetView(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return view, "", false
	}
	if err != nil {
		s.internalError(w, r, err)
		return view, "", false
	}
	role, ok := s.requireWorkspaceRole(w, r, view.WorkspaceID)
	if !ok {
		return view, "", false
	}
	if !view.Shared && view.OwnerID != currentSession(r).User.ID {
		notFound(w)
		return view, "", false
	}
	return view, role, true
}

// recordView logs a change to a view for the people who can see it: everyone
// if it is shared, otherwise only its owner.
func recordView(ctx context.Context, q *dbgen.Queries, view dbgen.View, action string) (int64, error) {
	var owner *uuid.UUID
	if !view.Shared {
		owner = &view.OwnerID
	}
	return record(ctx, q, view.WorkspaceID, api.SyncModelView, view.ID, action, viewRecord(view), nil, owner)
}

func encodeViewOptions(options map[string]any) ([]byte, *apiError) {
	if options == nil {
		options = map[string]any{}
	}
	encoded, err := json.Marshal(options)
	if err != nil || len(encoded) > maxViewOptionsBytes {
		return nil, badRequest("invalid_options", "Those view options are too large.")
	}
	return encoded, nil
}

// --- Favorites -------------------------------------------------------------

func (s *Server) CreateFavorite(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if _, ok := s.requireWorkspaceRole(w, r, workspaceId); !ok {
		return
	}
	var req api.CreateFavoriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	kinds := []api.FavoriteKind{api.FavoriteKindIssue, api.FavoriteKindProject, api.FavoriteKindView, api.FavoriteKindCycle}
	if req.Id == uuid.Nil || req.TargetId == uuid.Nil || !slices.Contains(kinds, req.Kind) {
		writeError(w, http.StatusBadRequest, "invalid_favorite", "That can't be added to favorites.")
		return
	}
	ctx := r.Context()
	userID := currentSession(r).User.ID

	var result api.FavoriteMutationResult
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		last, err := q.MaxFavoriteSortOrder(ctx, dbgen.MaxFavoriteSortOrderParams{WorkspaceID: workspaceId, UserID: userID})
		if err != nil {
			return err
		}
		favorite, err := q.CreateFavorite(ctx, dbgen.CreateFavoriteParams{
			ID: req.Id, WorkspaceID: workspaceId, UserID: userID, Kind: string(req.Kind), TargetID: req.TargetId, SortOrder: last + 1,
		})
		if db.IsNoRows(err) {
			// Either a retry, or this is already a favorite under another ID.
			created = false
			favorite, err = q.GetFavoriteByTarget(ctx, dbgen.GetFavoriteByTargetParams{UserID: userID, Kind: string(req.Kind), TargetID: req.TargetId})
			if db.IsNoRows(err) {
				return errIDTaken
			}
			if err == nil {
				result.Favorite = favoriteRecord(favorite)
				result.SyncId, err = q.GetLastSyncID(ctx, workspaceId)
			}
			return err
		}
		if err != nil {
			return err
		}
		result.Favorite = favoriteRecord(favorite)
		result.SyncId, err = record(ctx, q, workspaceId, api.SyncModelFavorite, favorite.ID, syncer.Insert, result.Favorite, nil, &userID)
		return err
	})
	s.respondCreated(w, r, err, created, result)
}

func (s *Server) DeleteFavorite(w http.ResponseWriter, r *http.Request, favoriteId uuid.UUID) {
	ctx := r.Context()
	userID := currentSession(r).User.ID
	favorite, err := s.q.GetFavorite(ctx, favoriteId)
	// Someone else's favorite is reported as missing.
	if db.IsNoRows(err) || (err == nil && (favorite.UserID != userID || !currentSession(r).allows(favorite.WorkspaceID))) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var ack api.SyncAck
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteFavorite(ctx, favoriteId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = record(ctx, q, favorite.WorkspaceID, api.SyncModelFavorite, favoriteId, syncer.Delete, favoriteRecord(favorite), nil, &userID)
		return err
	})
	s.respond(w, r, err, ack)
}

// --- Shared response helpers -------------------------------------------------

var errIDTaken = &apiError{http.StatusConflict, "id_taken", "That ID is already in use."}

// respond writes the result of a mutation, or its error.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, err error, result any) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// respondCreated is respond for creates, which answer 201 the first time and
// 200 when a retry finds the record already there.
func (s *Server) respondCreated(w http.ResponseWriter, r *http.Request, err error, created bool, result any) {
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
