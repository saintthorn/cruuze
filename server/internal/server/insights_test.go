package server

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

// completeWeeksAgo completes an issue and backdates when: started two days
// before it was completed, `weeks` full weeks before this one.
func completeWeeksAgo(t *testing.T, env *testEnv, c *client, issueID uuid.UUID, done api.WorkflowState, weeks int) {
	t.Helper()
	c.expect(http.StatusOK, "PATCH", "/api/issues/"+issueID.String(), map[string]any{"stateId": done.Id}, nil)
	// Wednesday noon of that week, so it sits clear of the week's edges.
	at := mondayOf(dateOf(time.Now())).Add(-time.Duration(weeks)*week + 60*time.Hour)
	_, err := env.pool.Exec(context.Background(),
		`UPDATE issues SET completed_at = $2::timestamptz, started_at = $2::timestamptz - interval '2 days', created_at = $2::timestamptz - interval '5 days' WHERE id = $1`, issueID, at)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTeamInsights(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	boot := ada.bootstrap(ws.Id)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)
	started := stateOfType(t, boot, team.Id, api.WorkflowStateTypeStarted)

	// Two issues completed in each of the last three full weeks, one in progress, one waiting.
	for weeksAgo := 1; weeksAgo <= 3; weeksAgo++ {
		for range 2 {
			completeWeeksAgo(t, env, ada, ada.createIssue(team.Id, "Done").Issue.Id, done, weeksAgo)
		}
	}
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+ada.createIssue(team.Id, "Doing").Issue.Id.String(), map[string]any{"stateId": started.Id}, nil)
	ada.createIssue(team.Id, "Waiting")

	var insights api.TeamInsights
	bob.expect(http.StatusOK, "GET", "/api/teams/"+team.Id.String()+"/insights", nil, &insights)
	if len(insights.Weeks) != 12 || len(insights.Flow) != 12 {
		t.Fatalf("%d weeks and %d flow points, want 12 of each", len(insights.Weeks), len(insights.Flow))
	}
	thisWeek := mondayOf(dateOf(time.Now()))
	if last := insights.Weeks[11]; !last.Start.Time.Equal(thisWeek) || last.Created != 2 || last.Completed != 0 {
		t.Errorf("current week = %+v, want it to start %s with 2 created and none completed", last, thisWeek.Format(time.DateOnly))
	}
	for i, want := range map[int]int32{10: 2, 9: 2, 8: 2, 7: 0} {
		if got := insights.Weeks[i].Completed; got != want {
			t.Errorf("week %d: %d completed, want %d", i, got, want)
		}
	}
	// Every completed issue took two days from start to done, five from filing.
	if c := insights.CycleTime; c.Samples != 6 || math.Abs(c.P50-2) > 0.01 || math.Abs(c.P85-2) > 0.01 || c.P95 == nil {
		t.Errorf("cycle time = %+v, want 6 samples at 2 days", c)
	}
	if l := insights.LeadTime; l.Samples != 6 || math.Abs(l.P50-5) > 0.01 {
		t.Errorf("lead time = %+v, want 6 samples at 5 days", l)
	}
	// Where everything stands now.
	if now := insights.Flow[11]; now.Completed != 6 || now.Started != 1 || now.Backlog != 1 {
		t.Errorf("flow now = %+v, want 6 completed, 1 started, 1 in the backlog", now)
	}

	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", "/api/teams/"+team.Id.String()+"/insights", nil, nil)
}

func TestForecasts(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, _, ws, team := workspaceWithMember(t, env)
	base := "/api/workspaces/" + ws.Id.String()
	boot := ada.bootstrap(ws.Id)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)

	target := time.Now().AddDate(0, 0, 21).Format(time.DateOnly)
	var project api.ProjectMutationResult
	ada.expect(http.StatusCreated, "POST", base+"/projects", map[string]any{"id": newID(), "name": "Launch", "targetDate": target}, &project)
	projectID := project.Project.Id
	inProject := func(teamID uuid.UUID, title string) api.Issue {
		var out api.IssueMutationResult
		ada.expect(http.StatusCreated, "POST", "/api/teams/"+teamID.String()+"/issues", map[string]any{"id": newID(), "title": title, "projectId": projectID}, &out)
		return out.Issue
	}
	forecastPath := "/api/projects/" + projectID.String() + "/forecast"

	// One completed issue is no pace to forecast from.
	completeWeeksAgo(t, env, ada, inProject(team.Id, "First").Id, done, 1)
	for range 6 {
		inProject(team.Id, "To do")
	}
	var forecast api.Forecast
	bob.expect(http.StatusOK, "GET", forecastPath, nil, &forecast)
	if forecast.EnoughHistory || forecast.P50 != nil || forecast.Remaining != 6 || forecast.Completed != 1 || forecast.TargetDate == nil {
		t.Fatalf("forecast from one completed issue = %+v", forecast)
	}

	// Two a week for the eight weeks before that: now there is.
	for weeksAgo := 2; weeksAgo <= 9; weeksAgo++ {
		for range 2 {
			completeWeeksAgo(t, env, ada, inProject(team.Id, "Done").Id, done, weeksAgo)
		}
	}
	// All of that was planned up front: the scope hasn't been growing.
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE issues SET created_at = now() - interval '200 days' WHERE project_id = $1 AND completed_at IS NOT NULL`, projectID); err != nil {
		t.Fatal(err)
	}
	bob.expect(http.StatusOK, "GET", forecastPath, nil, &forecast)
	if !forecast.EnoughHistory || forecast.Diverging || forecast.P50 == nil || forecast.P85 == nil || forecast.P95 == nil {
		t.Fatalf("forecast = %+v, want dates", forecast)
	}
	if forecast.Remaining != 6 || forecast.Completed != 17 || len(forecast.WeeklyCompleted) != 12 {
		t.Errorf("forecast scope = %d remaining, %d completed, %d weeks", forecast.Remaining, forecast.Completed, len(forecast.WeeklyCompleted))
	}
	if got := forecast.WeeklyCompleted[11]; got != 1 {
		t.Errorf("last full week: %d completed, want 1", got)
	}
	today := dateOf(time.Now())
	if forecast.P50.Time.Before(today) || forecast.P85.Time.Before(forecast.P50.Time) || forecast.P95.Time.Before(forecast.P85.Time) {
		t.Errorf("dates out of order: %s, %s, %s", forecast.P50, forecast.P85, forecast.P95)
	}
	// About 1.4 a week on average with 6 to go: a month or two.
	if weeks := forecast.P50.Time.Sub(today).Hours() / 24 / 7; weeks < 2 || weeks > 10 {
		t.Errorf("p50 is %.1f weeks out", weeks)
	}
	if forecast.OnTimeProbability == nil || *forecast.OnTimeProbability < 0 || *forecast.OnTimeProbability > 1 {
		t.Errorf("on-time probability = %v", forecast.OnTimeProbability)
	}
	// The same question gets the same answer.
	var again api.Forecast
	bob.expect(http.StatusOK, "GET", forecastPath, nil, &again)
	if !again.P85.Time.Equal(forecast.P85.Time) {
		t.Errorf("asking twice gave %s and %s", forecast.P85, again.P85)
	}

	// A milestone is a slice of the project, forecast at the project's pace.
	var milestone api.MilestoneMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/projects/"+projectID.String()+"/milestones", map[string]any{"id": newID(), "name": "Beta"}, &milestone)
	inMilestone := 0
	for _, i := range ada.bootstrap(ws.Id).Issues {
		if i.CompletedAt == nil && inMilestone < 2 {
			ada.expect(http.StatusOK, "PATCH", "/api/issues/"+i.Id.String(), map[string]any{"milestoneId": milestone.Milestone.Id}, nil)
			inMilestone++
		}
	}
	var slice api.Forecast
	bob.expect(http.StatusOK, "GET", forecastPath+"?milestoneId="+milestone.Milestone.Id.String(), nil, &slice)
	if slice.Remaining != 2 || !slice.EnoughHistory || slice.P50 == nil || slice.P50.Time.After(forecast.P50.Time) {
		t.Errorf("milestone forecast = %+v, want less remaining and no later than the project", slice)
	}
	bob.expect(http.StatusNotFound, "GET", forecastPath+"?milestoneId="+newID().String(), nil, nil)

	// Work in a team the asker can't see isn't counted for them.
	private := true
	var secret api.Team
	ada.expect(http.StatusCreated, "POST", base+"/teams", api.CreateTeamRequest{Name: "Secret", Key: "SEC", Private: &private}, &secret)
	inProject(secret.Id, "Hidden work")
	var adaView, bobView api.Forecast
	ada.expect(http.StatusOK, "GET", forecastPath, nil, &adaView)
	bob.expect(http.StatusOK, "GET", forecastPath, nil, &bobView)
	if adaView.Remaining != 7 || bobView.Remaining != 6 {
		t.Errorf("remaining: ada sees %d, bob %d; want 7 and 6", adaView.Remaining, bobView.Remaining)
	}
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "GET", forecastPath, nil, nil)

	// A cycle is forecast at the team's pace, against its end date.
	ada.expect(http.StatusOK, "PATCH", "/api/teams/"+team.Id.String(), map[string]any{"cycleWeeks": 2}, nil)
	cycle := ada.bootstrap(ws.Id).Cycles[0]
	var inCycle api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", "/api/teams/"+team.Id.String()+"/issues", map[string]any{"id": newID(), "title": "This cycle", "cycleId": cycle.Id}, &inCycle)
	var cycleForecast api.Forecast
	bob.expect(http.StatusOK, "GET", "/api/cycles/"+cycle.Id.String()+"/forecast", nil, &cycleForecast)
	if cycleForecast.Remaining != 1 || !cycleForecast.EnoughHistory || cycleForecast.P50 == nil ||
		cycleForecast.TargetDate == nil || !cycleForecast.TargetDate.Time.Equal(cycle.EndsAt.Time) || cycleForecast.OnTimeProbability == nil {
		t.Errorf("cycle forecast = %+v", cycleForecast)
	}
	eve.expect(http.StatusNotFound, "GET", "/api/cycles/"+cycle.Id.String()+"/forecast", nil, nil)
	bob.expect(http.StatusNotFound, "GET", "/api/cycles/"+newID().String()+"/forecast", nil, nil)
}
