package server

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const maxProjectUpdateLen = 10_000

var projectHealths = []api.ProjectHealth{api.ProjectHealthOnTrack, api.ProjectHealthAtRisk, api.ProjectHealthOffTrack}

func initiativeRecord(i dbgen.Initiative) api.Initiative {
	return api.Initiative{
		Id:          i.ID,
		Name:        i.Name,
		Description: i.Description,
		Color:       i.Color,
		TargetDate:  apiDate(i.TargetDate),
		CreatedAt:   i.CreatedAt,
	}
}

func projectDependencyRecord(d dbgen.ProjectDependency) api.ProjectDependency {
	return api.ProjectDependency{Id: d.ID, ProjectId: d.ProjectID, DependsOnId: d.DependsOnID, CreatedAt: d.CreatedAt}
}

func projectUpdateRecord(u dbgen.ProjectUpdate) api.ProjectUpdate {
	return api.ProjectUpdate{
		Id:        u.ID,
		ProjectId: u.ProjectID,
		UserId:    u.UserID,
		Health:    api.ProjectHealth(u.Health),
		Body:      u.Body,
		CreatedAt: u.CreatedAt,
	}
}

// checkInitiative makes sure an initiative a project is put in belongs to
// the project's workspace.
func checkInitiative(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	initiative, err := q.GetInitiative(ctx, *id)
	if db.IsNoRows(err) || (err == nil && initiative.WorkspaceID != workspaceID) {
		return badRequest("invalid_initiative", "That initiative doesn't exist in this workspace.")
	}
	return err
}

// --- Initiatives -----------------------------------------------------------

func (s *Server) CreateInitiative(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireEditor(w, r, workspaceId) {
		return
	}
	var req api.CreateInitiativeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := cleanName(req.Name)
	if !ok || req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_name", "Initiative name must be 1-80 characters.")
		return
	}
	params := dbgen.CreateInitiativeParams{
		ID:          req.Id,
		WorkspaceID: workspaceId,
		Name:        name,
		Color:       defaultProjectColor,
		TargetDate:  datePtr(req.TargetDate),
	}
	if req.Description != nil {
		params.Description = *req.Description
	}
	if req.Color != nil {
		params.Color = strings.ToLower(*req.Color)
	}
	if apiErr := validateInitiative(params.Description, params.Color); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		initiative, err := q.CreateInitiative(ctx, params)
		if db.IsNoRows(err) {
			// A retry: the initiative is already there.
			created = false
			if initiative, err = q.GetInitiative(ctx, req.Id); err == nil && initiative.WorkspaceID != workspaceId {
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
		ack.SyncId, err = record(ctx, q, workspaceId, api.SyncModelInitiative, initiative.ID, syncer.Insert, initiativeRecord(initiative), nil, nil)
		return err
	})
	s.respondCreated(w, r, err, created, ack)
}

func (s *Server) UpdateInitiative(w http.ResponseWriter, r *http.Request, initiativeId uuid.UUID) {
	initiative, ok := s.requireInitiative(w, r, initiativeId)
	if !ok {
		return
	}
	var req api.UpdateInitiativeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateInitiativeParams{ID: initiativeId, Description: req.Description}
	if req.Name != nil {
		name, ok := cleanName(*req.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_name", "Initiative name must be 1-80 characters.")
			return
		}
		params.Name = &name
	}
	color, description := initiative.Color, initiative.Description
	if req.Color != nil {
		color = strings.ToLower(*req.Color)
		params.Color = &color
	}
	if req.Description != nil {
		description = *req.Description
	}
	if apiErr := validateInitiative(description, color); apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	params.SetTargetDate, params.TargetDate = optionalDate(req.TargetDate)
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		updated, err := q.UpdateInitiative(ctx, params)
		if err != nil {
			return err
		}
		ack.SyncId, err = record(ctx, q, initiative.WorkspaceID, api.SyncModelInitiative, initiativeId, syncer.Update, initiativeRecord(updated), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

func (s *Server) DeleteInitiative(w http.ResponseWriter, r *http.Request, initiativeId uuid.UUID) {
	initiative, ok := s.requireInitiative(w, r, initiativeId)
	if !ok {
		return
	}
	ctx := r.Context()
	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		projects, err := q.ListProjectsOfInitiative(ctx, &initiativeId)
		if err != nil {
			return err
		}
		if err := q.DeleteInitiative(ctx, initiativeId); err != nil {
			return err
		}
		// Its projects stay, now on their own.
		for _, project := range projects {
			project.InitiativeID = nil
			if _, err := record(ctx, q, initiative.WorkspaceID, api.SyncModelProject, project.ID, syncer.Update, projectRecord(project), nil, nil); err != nil {
				return err
			}
		}
		ack.SyncId, err = record(ctx, q, initiative.WorkspaceID, api.SyncModelInitiative, initiativeId, syncer.Delete, initiativeRecord(initiative), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

// requireInitiative loads an initiative the caller may manage.
func (s *Server) requireInitiative(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Initiative, bool) {
	initiative, err := s.q.GetInitiative(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return initiative, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return initiative, false
	}
	return initiative, s.requireEditor(w, r, initiative.WorkspaceID)
}

func validateInitiative(description, color string) *apiError {
	if len(description) > maxIssueDescriptionLen {
		return badRequest("invalid_description", "Initiative description is too long.")
	}
	return validateColor(color)
}

// --- Project dependencies ----------------------------------------------------

func (s *Server) CreateProjectDependency(w http.ResponseWriter, r *http.Request, projectId uuid.UUID) {
	project, ok := s.requireProject(w, r, projectId)
	if !ok {
		return
	}
	var req api.CreateProjectDependencyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Id == uuid.Nil || req.DependsOnId == projectId {
		writeError(w, http.StatusBadRequest, "invalid_dependency", "A project can't depend on itself.")
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		// One at a time per workspace, so two requests can't close a loop between them.
		if err := q.LockWorkspace(ctx, project.WorkspaceID); err != nil {
			return err
		}
		other, err := q.GetProject(ctx, req.DependsOnId)
		if db.IsNoRows(err) || (err == nil && other.WorkspaceID != project.WorkspaceID) {
			return badRequest("invalid_dependency", "That project doesn't exist in this workspace.")
		}
		if err != nil {
			return err
		}
		existing, err := q.ListProjectDependencies(ctx, project.WorkspaceID)
		if err != nil {
			return err
		}
		for _, d := range existing {
			if d.ID == req.Id || (d.ProjectID == projectId && d.DependsOnID == req.DependsOnId) {
				if d.ProjectID != projectId || d.DependsOnID != req.DependsOnId {
					return errIDTaken
				}
				// A retry, or the same thing said twice.
				created = false
				ack.SyncId, err = q.GetLastSyncID(ctx, project.WorkspaceID)
				return err
			}
		}
		if dependsOn(existing, req.DependsOnId, projectId) {
			return &apiError{http.StatusConflict, "dependency_loop", "That would make the projects wait on each other."}
		}
		dependency, err := q.CreateProjectDependency(ctx, dbgen.CreateProjectDependencyParams{
			ID:          req.Id,
			WorkspaceID: project.WorkspaceID,
			ProjectID:   projectId,
			DependsOnID: req.DependsOnId,
		})
		if db.IsNoRows(err) {
			return errIDTaken
		}
		if err != nil {
			return err
		}
		ack.SyncId, err = record(ctx, q, project.WorkspaceID, api.SyncModelProjectDependency, dependency.ID, syncer.Insert, projectDependencyRecord(dependency), nil, nil)
		return err
	})
	s.respondCreated(w, r, err, created, ack)
}

// dependsOn reports whether project from depends on project to, directly or
// through others.
func dependsOn(dependencies []dbgen.ProjectDependency, from, to uuid.UUID) bool {
	seen := map[uuid.UUID]bool{}
	queue := []uuid.UUID{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == to {
			return true
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		for _, d := range dependencies {
			if d.ProjectID == current {
				queue = append(queue, d.DependsOnID)
			}
		}
	}
	return false
}

func (s *Server) DeleteProjectDependency(w http.ResponseWriter, r *http.Request, dependencyId uuid.UUID) {
	ctx := r.Context()
	dependency, err := s.q.GetProjectDependency(ctx, dependencyId)
	if db.IsNoRows(err) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !s.requireEditor(w, r, dependency.WorkspaceID) {
		return
	}
	var ack api.SyncAck
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteProjectDependency(ctx, dependencyId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = record(ctx, q, dependency.WorkspaceID, api.SyncModelProjectDependency, dependencyId, syncer.Delete, projectDependencyRecord(dependency), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

// --- Project updates ---------------------------------------------------------

func (s *Server) CreateProjectUpdate(w http.ResponseWriter, r *http.Request, projectId uuid.UUID) {
	project, ok := s.requireProject(w, r, projectId)
	if !ok {
		return
	}
	var req api.CreateProjectUpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID := currentSession(r).User.ID
	params := dbgen.CreateProjectUpdateParams{
		ID:          req.Id,
		WorkspaceID: project.WorkspaceID,
		ProjectID:   projectId,
		UserID:      &userID,
		Health:      string(req.Health),
	}
	if req.Body != nil {
		params.Body = strings.TrimSpace(*req.Body)
	}
	if req.Id == uuid.Nil || !slices.Contains(projectHealths, req.Health) || len(params.Body) > maxProjectUpdateLen {
		writeError(w, http.StatusBadRequest, "invalid_update", "An update says whether the project is on track, at risk or off track, in at most 10,000 characters.")
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		update, err := q.CreateProjectUpdate(ctx, params)
		if db.IsNoRows(err) {
			created = false
			if update, err = q.GetProjectUpdate(ctx, req.Id); err == nil && update.ProjectID != projectId {
				return errIDTaken
			}
			if err == nil {
				ack.SyncId, err = q.GetLastSyncID(ctx, project.WorkspaceID)
			}
			return err
		}
		if err != nil {
			return err
		}
		ack.SyncId, err = record(ctx, q, project.WorkspaceID, api.SyncModelProjectUpdate, update.ID, syncer.Insert, projectUpdateRecord(update), nil, nil)
		return err
	})
	s.respondCreated(w, r, err, created, ack)
}

func (s *Server) DeleteProjectUpdate(w http.ResponseWriter, r *http.Request, updateId uuid.UUID) {
	ctx := r.Context()
	update, err := s.q.GetProjectUpdate(ctx, updateId)
	if db.IsNoRows(err) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	role, ok := s.requireWorkspaceRole(w, r, update.WorkspaceID)
	if !ok {
		return
	}
	isAuthor := update.UserID != nil && *update.UserID == currentSession(r).User.ID
	if role == roleGuest || (!isAuthor && role != roleAdmin) {
		forbidden(w)
		return
	}
	var ack api.SyncAck
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteProjectUpdate(ctx, updateId); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = record(ctx, q, update.WorkspaceID, api.SyncModelProjectUpdate, updateId, syncer.Delete, projectUpdateRecord(update), nil, nil)
		return err
	})
	s.respond(w, r, err, ack)
}
