package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

func (c *client) timeEntries(workspaceID uuid.UUID) []api.TimeEntry {
	c.t.Helper()
	return c.bootstrap(workspaceID).TimeEntries
}

func TestTimeTracking(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, adaMe, bobMe, ws, team := workspaceWithMember(t, env)
	first := ada.createIssue(team.Id, "Build it").Issue
	second := ada.createIssue(team.Id, "Test it").Issue
	entriesPath := func(issueID uuid.UUID) string { return "/api/issues/" + issueID.String() + "/time-entries" }
	now := time.Now().UTC().Truncate(time.Second)

	// A finished stretch counts at once, and everyone sees the total.
	logged := uuid.New()
	body := map[string]any{"id": logged, "startedAt": now.Add(-2 * time.Hour), "endedAt": now.Add(-time.Hour), "seconds": 1800, "note": "pairing"}
	ada.expect(http.StatusCreated, "POST", entriesPath(first.Id), body, nil)
	ada.expect(http.StatusOK, "POST", entriesPath(first.Id), body, nil) // a retry
	if got := bob.issue(ws.Id, first.Id).LoggedSeconds; got == nil || *got != 1800 {
		t.Errorf("logged total seen by bob = %v, want 1800", got)
	}
	// But the entries are their owner's alone.
	if mine := ada.timeEntries(ws.Id); len(mine) != 1 || mine[0].UserId != adaMe.User.Id || mine[0].Note != "pairing" {
		t.Errorf("ada's entries = %+v", mine)
	}
	if theirs := bob.timeEntries(ws.Id); len(theirs) != 0 {
		t.Errorf("bob sees ada's entries: %+v", theirs)
	}
	batch := env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, 0)
	if got := only(batch, api.SyncModelTimeEntry); len(got) != 0 {
		t.Errorf("bob's sync feed has ada's time entries: %v", got)
	}
	bob.expect(http.StatusNotFound, "PATCH", "/api/time-entries/"+logged.String(), map[string]any{"note": "mine now"}, nil)
	bob.expect(http.StatusNotFound, "DELETE", "/api/time-entries/"+logged.String(), nil, nil)

	// What can't be right is refused.
	for name, bad := range map[string]map[string]any{
		"longer than its stretch": {"id": uuid.New(), "startedAt": now.Add(-time.Hour), "endedAt": now, "seconds": 7200},
		"nothing":                 {"id": uuid.New(), "startedAt": now.Add(-time.Hour), "endedAt": now, "seconds": 0},
		"in the future":           {"id": uuid.New(), "startedAt": now.Add(time.Hour)},
		"running with a duration": {"id": uuid.New(), "startedAt": now, "seconds": 60},
	} {
		if status := ada.do("POST", entriesPath(first.Id), bad, nil); status != http.StatusBadRequest {
			t.Errorf("entry %s: status %d, want 400", name, status)
		}
	}

	// A timer runs until it is stopped; starting another stops it.
	timer := uuid.New()
	ada.expect(http.StatusCreated, "POST", entriesPath(first.Id), map[string]any{"id": timer, "startedAt": now.Add(-10 * time.Minute)}, nil)
	if got := ada.issue(ws.Id, first.Id).LoggedSeconds; got == nil || *got != 1800 {
		t.Errorf("a running timer changed the total: %v", got)
	}
	next := uuid.New()
	ada.expect(http.StatusCreated, "POST", entriesPath(second.Id), map[string]any{"id": next, "startedAt": now}, nil)
	running := 0
	for _, e := range ada.timeEntries(ws.Id) {
		if e.EndedAt == nil {
			running++
			if e.Id != next {
				t.Errorf("the running timer is %s, want the newest", e.Id)
			}
		}
		if e.Id == timer && (e.EndedAt == nil || e.Seconds < 590 || e.Seconds > 660) {
			t.Errorf("the replaced timer = %+v, want about ten minutes", e)
		}
	}
	if running != 1 {
		t.Errorf("%d timers running, want 1", running)
	}
	if got := ada.issue(ws.Id, first.Id).LoggedSeconds; got == nil || *got < 1800+590 {
		t.Errorf("total after the timer stopped = %v", got)
	}
	// Bob's timer is his own business.
	bobTimer := uuid.New()
	bob.expect(http.StatusCreated, "POST", entriesPath(second.Id), map[string]any{"id": bobTimer, "startedAt": now}, nil)
	if entries := ada.timeEntries(ws.Id); len(entries) != 3 {
		t.Errorf("ada has %d entries after bob started a timer, want 3", len(entries))
	}

	// Stopping, with the time the client measured.
	ada.expect(http.StatusBadRequest, "PATCH", "/api/time-entries/"+next.String(), map[string]any{"seconds": 30}, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/time-entries/"+next.String(), map[string]any{"endedAt": now.Add(30 * time.Second)}, nil)
	if got := ada.issue(ws.Id, second.Id).LoggedSeconds; got == nil || *got != 30 {
		t.Errorf("total on the second issue = %v, want 30", got)
	}
	// Correcting it afterwards.
	ada.expect(http.StatusOK, "PATCH", "/api/time-entries/"+next.String(), map[string]any{"seconds": 20, "note": "less than I thought"}, nil)
	ada.expect(http.StatusBadRequest, "PATCH", "/api/time-entries/"+next.String(), map[string]any{"seconds": 3600}, nil)
	if got := ada.issue(ws.Id, second.Id).LoggedSeconds; got == nil || *got != 20 {
		t.Errorf("total after the correction = %v, want 20", got)
	}

	// Exports: an admin gets everyone's finished time, a member their own.
	bob.expect(http.StatusOK, "PATCH", "/api/time-entries/"+bobTimer.String(), map[string]any{"endedAt": now.Add(time.Minute), "note": "=HYPERLINK(1)"}, nil)
	day := now.Format(time.DateOnly)
	export := func(c *client, query string) string {
		t.Helper()
		res := c.raw("GET", "/api/workspaces/"+ws.Id.String()+"/time-entries.csv?"+query, nil, "")
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("export %s: status %d", query, res.StatusCode)
		}
		text, _ := io.ReadAll(res.Body)
		return string(text)
	}
	all := export(ada, "from="+day+"&to="+day)
	if lines := strings.Count(strings.TrimSpace(all), "\n"); lines != 4 {
		t.Errorf("admin export has %d rows, want 4:\n%s", lines, all)
	}
	if !strings.Contains(all, bobMe.User.Email) || !strings.Contains(all, "'=HYPERLINK(1)") || !strings.Contains(all, team.Key+"-1,Build it,0.50,1800,pairing") {
		t.Errorf("admin export:\n%s", all)
	}
	own := export(bob, "from="+day+"&to="+day)
	if strings.Contains(own, adaMe.User.Email) || strings.Count(strings.TrimSpace(own), "\n") != 1 {
		t.Errorf("member export:\n%s", own)
	}
	if status := ada.do("GET", "/api/workspaces/"+ws.Id.String()+"/time-entries.csv?from=2026-01-01&to=2028-01-01", nil, nil); status != http.StatusBadRequest {
		t.Errorf("a two-year export: status %d, want 400", status)
	}

	// Deleting takes the time back off the issue.
	ada.expect(http.StatusOK, "DELETE", "/api/time-entries/"+logged.String(), nil, nil)
	if got := ada.issue(ws.Id, first.Id).LoggedSeconds; got == nil || *got >= 1800 {
		t.Errorf("total after deleting the logged entry = %v", got)
	}

	// Time in progress comes from the status history, with no timer at all.
	boot := ada.bootstrap(ws.Id)
	started := stateOfType(t, boot, team.Id, api.WorkflowStateTypeStarted)
	third := ada.createIssue(team.Id, "Untimed").Issue
	if got := ada.details("/api/issues/" + third.Id.String()).InProgressSeconds; got != 0 {
		t.Errorf("in progress before starting = %d", got)
	}
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+third.Id.String(), map[string]any{"stateId": started.Id}, nil)
	if _, err := env.pool.Exec(t.Context(), `UPDATE issue_state_history SET entered_at = now() - interval '3 hours' WHERE issue_id = $1 AND left_at IS NULL`, third.Id); err != nil {
		t.Fatal(err)
	}
	if got := ada.details("/api/issues/" + third.Id.String()).InProgressSeconds; got < 3*3600-5 || got > 3*3600+60 {
		t.Errorf("in progress = %d, want about three hours", got)
	}
}
