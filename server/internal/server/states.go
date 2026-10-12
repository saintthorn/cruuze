package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	maxStateNameLen = 40
	maxTeamStates   = 24
)

// customStateTypes are the kinds of status a team can add. Triage is not
// among them: it comes and goes with the team's triage setting.
var customStateTypes = []api.WorkflowStateType{
	api.WorkflowStateTypeBacklog, api.WorkflowStateTypeUnstarted, api.WorkflowStateTypeStarted,
	api.WorkflowStateTypeCompleted, api.WorkflowStateTypeCanceled,
}

func (s *Server) CreateWorkflowState(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	team, ok := s.requireTeamManager(w, r, teamId)
	if !ok {
		return
	}
	var req api.CreateWorkflowStateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, apiErr := cleanStateName(req.Name)
	if apiErr == nil {
		apiErr = validateColor(req.Color)
	}
	if apiErr == nil && (req.Id == uuid.Nil || !slices.Contains(customStateTypes, req.Type)) {
		apiErr = badRequest("invalid_state", "A status needs an ID and a type of backlog, unstarted, started, completed or canceled.")
	}
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	created := true
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		states, err := q.ListWorkflowStatesForTeam(ctx, teamId)
		if err != nil {
			return err
		}
		for _, existing := range states {
			if existing.ID == req.Id {
				// A retry of a create that already went through.
				created = false
				ack.SyncId, err = q.GetLastSyncID(ctx, team.WorkspaceID)
				return err
			}
		}
		if err := checkStateName(states, name, uuid.Nil); err != nil {
			return err
		}
		if len(states) >= maxTeamStates {
			return badRequest("too_many_states", "This team has too many statuses. Delete one first.")
		}
		position := 0.0
		if req.Position != nil {
			position = *req.Position
		} else if len(states) > 0 {
			position = states[len(states)-1].Position + 1
		}
		state, err := q.CreateCustomWorkflowState(ctx, dbgen.CreateCustomWorkflowStateParams{
			ID:          req.Id,
			WorkspaceID: team.WorkspaceID,
			TeamID:      teamId,
			Name:        name,
			Type:        string(req.Type),
			Color:       req.Color,
			Position:    position,
		})
		if db.IsNoRows(err) {
			return errIDTaken // the ID belongs to another team's status
		}
		if err != nil {
			return err
		}
		ack.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelWorkflowState, state.ID, syncer.Insert, workflowStateRecord(state), &team, nil)
		return err
	})
	s.respondCreated(w, r, err, created, ack)
}

func (s *Server) UpdateWorkflowState(w http.ResponseWriter, r *http.Request, stateId uuid.UUID) {
	state, team, ok := s.requireState(w, r, stateId)
	if !ok {
		return
	}
	var req api.UpdateWorkflowStateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateWorkflowStateParams{ID: state.ID, Color: req.Color, Position: req.Position}
	if req.Name != nil {
		name, apiErr := cleanStateName(*req.Name)
		if apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		params.Name = &name
	}
	if req.Color != nil {
		if apiErr := validateColor(*req.Color); apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if params.Name != nil {
			states, err := q.ListWorkflowStatesForTeam(ctx, team.ID)
			if err != nil {
				return err
			}
			if err := checkStateName(states, *params.Name, state.ID); err != nil {
				return err
			}
		}
		updated, err := q.UpdateWorkflowState(ctx, params)
		if err != nil {
			return err
		}
		ack.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelWorkflowState, updated.ID, syncer.Update, workflowStateRecord(updated), &team, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

func (s *Server) DeleteWorkflowState(w http.ResponseWriter, r *http.Request, stateId uuid.UUID) {
	state, team, ok := s.requireState(w, r, stateId)
	if !ok {
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if state.Type == string(api.WorkflowStateTypeTriage) {
			return badRequest("triage_state", "Triage is removed by turning triage off in the team's settings.")
		}
		states, err := q.ListWorkflowStatesForTeam(ctx, team.ID)
		if err != nil {
			return err
		}
		// New issues need somewhere to start, and finished ones somewhere to end.
		open, completed := 0, 0
		for _, other := range states {
			if other.ID == state.ID {
				continue
			}
			switch api.WorkflowStateType(other.Type) {
			case api.WorkflowStateTypeBacklog, api.WorkflowStateTypeUnstarted:
				open++
			case api.WorkflowStateTypeCompleted:
				completed++
			}
		}
		if open == 0 || completed == 0 {
			return &apiError{http.StatusConflict, "last_state", "A team needs at least one status for new issues and one for completed ones."}
		}
		inUse, err := q.CountIssuesInState(ctx, state.ID)
		if err != nil {
			return err
		}
		if inUse > 0 {
			return &apiError{http.StatusConflict, "state_in_use", "Move the issues in this status (archived ones included) to another one first."}
		}
		if err := q.DeleteWorkflowState(ctx, state.ID); err != nil {
			return err
		}
		ack.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelWorkflowState, state.ID, syncer.Delete, workflowStateRecord(state), &team, nil)
		if err != nil || !equalPtr(team.ReviewStateID, &state.ID) {
			return err
		}
		// It was the team's review status; the database has cleared that
		// setting, and clients need to hear it.
		updated, err := q.GetTeam(ctx, team.ID)
		if err != nil {
			return err
		}
		ack.SyncId, err = record(ctx, q, team.WorkspaceID, api.SyncModelTeam, team.ID, syncer.Update, teamRecord(updated), &updated, nil)
		return err
	})
	s.respond(w, r, err, ack)
}

// requireTeamManager loads a team the caller may configure: its members,
// unless they are guests, and workspace admins.
func (s *Server) requireTeamManager(w http.ResponseWriter, r *http.Request, teamID uuid.UUID) (dbgen.Team, bool) {
	access, ok := s.requireTeamAccess(w, r, teamID)
	if !ok {
		return dbgen.Team{}, false
	}
	if access.role == roleGuest || (!access.isMember && access.role != roleAdmin) {
		forbidden(w)
		return dbgen.Team{}, false
	}
	return access.team, true
}

func (s *Server) requireState(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.WorkflowState, dbgen.Team, bool) {
	state, err := s.q.GetWorkflowState(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return state, dbgen.Team{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return state, dbgen.Team{}, false
	}
	team, ok := s.requireTeamManager(w, r, state.TeamID)
	return state, team, ok
}

func cleanStateName(raw string) (string, *apiError) {
	name := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxStateNameLen {
		return "", badRequest("invalid_name", "Status name must be 1-40 characters.")
	}
	return name, nil
}

// checkStateName refuses a name another of the team's statuses already has.
func checkStateName(states []dbgen.WorkflowState, name string, except uuid.UUID) error {
	for _, other := range states {
		if other.ID != except && strings.EqualFold(other.Name, name) {
			return &apiError{http.StatusConflict, "state_name_taken", "Another status of this team already has that name."}
		}
	}
	return nil
}

// firstStateOfType returns the team's first state of a type, by position.
func firstStateOfType(ctx context.Context, q *dbgen.Queries, teamID uuid.UUID, kind api.WorkflowStateType) (dbgen.WorkflowState, bool, error) {
	state, err := q.GetFirstWorkflowStateOfType(ctx, dbgen.GetFirstWorkflowStateOfTypeParams{TeamID: teamID, Type: string(kind)})
	if db.IsNoRows(err) {
		return state, false, nil
	}
	return state, err == nil, err
}
