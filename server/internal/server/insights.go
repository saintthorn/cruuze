package server

import (
	"context"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/forecast"
)

const (
	week = 7 * 24 * time.Hour
	// insightWeeks is how far back reports look.
	insightWeeks = 12
	// forecastWeeks of completed work are what forecasts draw on.
	forecastWeeks = 12
	// calibrationWeeks of history are replayed to check the method.
	calibrationWeeks = 26
	// Past forecasts are checked the way they'd have been made: from eight
	// weeks of history, about the four weeks that followed.
	calibrationLookback, calibrationHorizon = 8, 4
)

// weekRow is a count of issues for one week, as the queries return it.
type weekRow struct {
	week   time.Time
	issues int32
}

// weeklySeries lays query rows out as one count per week from first for n
// weeks; weeks without a row are zero.
func weeklySeries(rows []weekRow, first time.Time, n int) []int {
	series := make([]int, n)
	for _, row := range rows {
		index := int(dateOf(row.week).Sub(first) / week)
		if index >= 0 && index < n {
			series[index] = int(row.issues)
		}
	}
	return series
}

// newForecastRand returns a source of randomness seeded from what is being
// forecast, so asking twice about the same data gives the same answer
// rather than dates that jitter on every load.
func newForecastRand(scope uuid.UUID, remaining int) *rand.Rand {
	var seed uint64
	for _, b := range scope[:8] {
		seed = seed<<8 | uint64(b)
	}
	return rand.New(rand.NewPCG(seed, uint64(remaining)))
}

func calibration(done []int, scope uuid.UUID) api.Calibration {
	windows, held := forecast.Calibrate(done, calibrationLookback, calibrationHorizon, newForecastRand(scope, 0))
	return api.Calibration{Windows: int32(windows), Held: int32(held)}
}

func (s *Server) GetTeamInsights(w http.ResponseWriter, r *http.Request, teamId api.TeamId) {
	if _, ok := s.requireTeamAccess(w, r, teamId); !ok {
		return
	}
	ctx := r.Context()
	thisWeek := mondayOf(dateOf(time.Now()))
	first := thisWeek.Add(-(insightWeeks - 1) * week)

	out := api.TeamInsights{}
	err := func() error {
		completed, err := s.q.WeeklyCompletedForTeam(ctx, dbgen.WeeklyCompletedForTeamParams{TeamID: teamId, Since: thisWeek.Add(-calibrationWeeks * week)})
		if err != nil {
			return err
		}
		created, err := s.q.WeeklyCreatedForTeam(ctx, dbgen.WeeklyCreatedForTeamParams{TeamID: teamId, Since: first})
		if err != nil {
			return err
		}
		completedRows := make([]weekRow, len(completed))
		for i, row := range completed {
			completedRows[i] = weekRow{row.Week, row.Issues}
		}
		createdRows := make([]weekRow, len(created))
		for i, row := range created {
			createdRows[i] = weekRow{row.Week, row.Issues}
		}
		doneSeries := weeklySeries(completedRows, first, insightWeeks)
		createdSeries := weeklySeries(createdRows, first, insightWeeks)
		for i := range insightWeeks {
			out.Weeks = append(out.Weeks, struct {
				Completed int32              `json:"completed"`
				Created   int32              `json:"created"`
				Start     openapi_types.Date `json:"start"`
			}{
				Start:     openapi_types.Date{Time: first.Add(time.Duration(i) * week)},
				Created:   int32(createdSeries[i]),
				Completed: int32(doneSeries[i]),
			})
		}
		// Full weeks only: the current one would read as a slow week.
		out.Calibration = calibration(weeklySeries(completedRows, thisWeek.Add(-calibrationWeeks*week), calibrationWeeks), teamId)

		times, err := s.q.TeamCycleTimes(ctx, dbgen.TeamCycleTimesParams{TeamID: teamId, Since: first})
		if err != nil {
			return err
		}
		out.CycleTime = api.Durations{Samples: times.CycleSamples, P50: times.CycleP50, P85: times.CycleP85, P95: &times.CycleP95}
		out.LeadTime = api.Durations{Samples: times.LeadSamples, P50: times.LeadP50, P85: times.LeadP85}

		// The state of things at the end of each week; for the current week, as of now.
		now := time.Now()
		flow, err := s.q.TeamFlow(ctx, dbgen.TeamFlowParams{TeamID: teamId, First: first.Add(week), Last: thisWeek.Add(week)})
		if err != nil {
			return err
		}
		type point = struct {
			Backlog   int32              `json:"backlog"`
			Canceled  int32              `json:"canceled"`
			Completed int32              `json:"completed"`
			Date      openapi_types.Date `json:"date"`
			Started   int32              `json:"started"`
			Unstarted int32              `json:"unstarted"`
		}
		points := make([]point, insightWeeks)
		for i := range points {
			end := first.Add(time.Duration(i+1) * week)
			if end.After(now) {
				end = dateOf(now)
			}
			points[i].Date = openapi_types.Date{Time: end}
		}
		for _, row := range flow {
			index := int(dateOf(row.Day).Sub(first)/week) - 1
			if index < 0 || index >= insightWeeks {
				continue
			}
			switch api.WorkflowStateType(row.StateType) {
			case api.WorkflowStateTypeBacklog, api.WorkflowStateTypeTriage:
				points[index].Backlog += row.Issues
			case api.WorkflowStateTypeUnstarted:
				points[index].Unstarted += row.Issues
			case api.WorkflowStateTypeStarted:
				points[index].Started += row.Issues
			case api.WorkflowStateTypeCompleted:
				points[index].Completed += row.Issues
			case api.WorkflowStateTypeCanceled:
				points[index].Canceled += row.Issues
			}
		}
		out.Flow = points
		return nil
	}()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// forecastInput is what a forecast is made from.
type forecastInput struct {
	scope                uuid.UUID
	remaining, completed int32
	// done and added are weekly counts, oldest first, over calibrationWeeks
	// full weeks; added is nil for a scope that isn't expected to grow.
	done, added []int
	target      *time.Time
}

// makeForecast runs the simulation and puts the answer in API form.
func makeForecast(in forecastInput, today time.Time) api.Forecast {
	recent := in.done[len(in.done)-forecastWeeks:]
	out := api.Forecast{
		Remaining:       in.remaining,
		Completed:       in.completed,
		WeeklyCompleted: make([]int32, len(recent)),
		EnoughHistory:   forecast.Enough(recent),
		Calibration:     calibration(in.done, in.scope),
	}
	for i, n := range recent {
		out.WeeklyCompleted[i] = int32(n)
	}
	if in.target != nil {
		out.TargetDate = &openapi_types.Date{Time: *in.target}
	}
	if !out.EnoughHistory {
		return out
	}
	var added []int
	if in.added != nil {
		added = in.added[len(in.added)-forecastWeeks:]
	}
	days := forecast.Simulate(int(in.remaining), recent, added, newForecastRand(in.scope, int(in.remaining)))
	if in.target != nil {
		chance := forecast.ProbabilityBy(days, int(dateOf(*in.target).Sub(today)/(24*time.Hour)))
		out.OnTimeProbability = &chance
	}
	// If even the optimistic trials don't finish, dates would be fiction.
	if forecast.Percentile(days, 0.5) >= forecast.Never {
		out.Diverging = true
		return out
	}
	date := func(q float64) *openapi_types.Date {
		d := forecast.Percentile(days, q)
		if d >= forecast.Never {
			return nil
		}
		return &openapi_types.Date{Time: today.AddDate(0, 0, d)}
	}
	out.P50, out.P85, out.P95 = date(0.5), date(0.85), date(0.95)
	return out
}

func (s *Server) GetProjectForecast(w http.ResponseWriter, r *http.Request, projectId uuid.UUID, params api.GetProjectForecastParams) {
	project, ok := s.requireProjectViewer(w, r, projectId)
	if !ok {
		return
	}
	ctx := r.Context()
	in := forecastInput{scope: projectId, target: project.TargetDate}
	if params.MilestoneId != nil {
		milestone, err := s.q.GetMilestone(ctx, *params.MilestoneId)
		if db.IsNoRows(err) || (err == nil && milestone.ProjectID != projectId) {
			notFound(w)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		in.scope, in.target = milestone.ID, milestone.TargetDate
	}
	teamIDs, err := s.visibleTeamIDs(ctx, r, project.WorkspaceID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	today := dateOf(time.Now())
	first := mondayOf(today).Add(-calibrationWeeks * week)

	err = func() error {
		scope, err := s.q.ProjectScope(ctx, dbgen.ProjectScopeParams{ProjectID: &projectId, TeamIds: teamIDs, MilestoneID: params.MilestoneId})
		if err != nil {
			return err
		}
		in.remaining, in.completed = scope.Remaining, scope.Completed
		// The pace is the whole project's, also when asking about one
		// milestone: the same people work through them in turn.
		done, err := s.q.WeeklyCompletedForProject(ctx, dbgen.WeeklyCompletedForProjectParams{ProjectID: &projectId, TeamIds: teamIDs, Since: first})
		if err != nil {
			return err
		}
		created, err := s.q.WeeklyCreatedForProject(ctx, dbgen.WeeklyCreatedForProjectParams{ProjectID: &projectId, TeamIds: teamIDs, Since: first})
		if err != nil {
			return err
		}
		doneRows := make([]weekRow, len(done))
		for i, row := range done {
			doneRows[i] = weekRow{row.Week, row.Issues}
		}
		createdRows := make([]weekRow, len(created))
		for i, row := range created {
			createdRows[i] = weekRow{row.Week, row.Issues}
		}
		in.done = weeklySeries(doneRows, first, calibrationWeeks)
		// Projects grow as they go; a milestone's scope is taken as settled.
		if params.MilestoneId == nil {
			in.added = weeklySeries(createdRows, first, calibrationWeeks)
		}
		return nil
	}()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, makeForecast(in, today))
}

func (s *Server) GetCycleForecast(w http.ResponseWriter, r *http.Request, cycleId uuid.UUID) {
	ctx := r.Context()
	cycle, err := s.q.GetCycle(ctx, cycleId)
	if db.IsNoRows(err) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if _, ok := s.requireTeamAccess(w, r, cycle.TeamID); !ok {
		return
	}
	today := dateOf(time.Now())
	first := mondayOf(today).Add(-calibrationWeeks * week)

	in := forecastInput{scope: cycleId, target: &cycle.EndsAt}
	err = func() error {
		scope, err := s.q.CycleScope(ctx, &cycleId)
		if err != nil {
			return err
		}
		in.remaining, in.completed = scope.Remaining, scope.Completed
		// A cycle is the team's whole focus, so the pace is the team's.
		done, err := s.q.WeeklyCompletedForTeam(ctx, dbgen.WeeklyCompletedForTeamParams{TeamID: cycle.TeamID, Since: first})
		if err != nil {
			return err
		}
		rows := make([]weekRow, len(done))
		for i, row := range done {
			rows[i] = weekRow{row.Week, row.Issues}
		}
		in.done = weeklySeries(rows, first, calibrationWeeks)
		return nil
	}()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, makeForecast(in, today))
}

// requireProjectViewer loads a project of a workspace the caller belongs to.
func (s *Server) requireProjectViewer(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Project, bool) {
	project, err := s.q.GetProject(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return project, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return project, false
	}
	_, ok := s.requireWorkspaceRole(w, r, project.WorkspaceID)
	return project, ok
}

// visibleTeamIDs lists the teams whose issues the caller may see: what
// reports about a workspace-wide thing, like a project, are limited to.
func (s *Server) visibleTeamIDs(ctx context.Context, r *http.Request, workspaceID uuid.UUID) ([]uuid.UUID, error) {
	userID := currentSession(r).User.ID
	role, err := s.q.GetWorkspaceRole(ctx, dbgen.GetWorkspaceRoleParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return nil, err
	}
	teams, err := s.q.ListTeamsForUser(ctx, dbgen.ListTeamsForUserParams{WorkspaceID: workspaceID, UserID: userID, MembersOnly: role == roleGuest})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(teams))
	for _, t := range teams {
		ids = append(ids, t.Team.ID)
	}
	return ids, nil
}
