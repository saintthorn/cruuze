package server

import (
	"context"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	// How many cycles are kept ready beyond the current one.
	upcomingCycles = 2
	cycleKeepEvery = time.Hour
)

// KeepCycles runs cycle upkeep now and then hourly until ctx is done: it
// creates each team's next cycles and moves unfinished work out of cycles
// that have ended.
func (s *Server) KeepCycles(ctx context.Context) {
	ticker := time.NewTicker(cycleKeepEvery)
	defer ticker.Stop()
	for {
		if err := s.keepAllCycles(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.log.Warn("cycle upkeep", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) keepAllCycles(ctx context.Context, now time.Time) error {
	teams, err := s.q.ListTeamsWithCycles(ctx)
	if err != nil {
		return err
	}
	for _, team := range teams {
		err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
			return keepTeamCycles(ctx, q, team, now)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// keepTeamCycles makes sure the team has a current cycle and its upcoming
// ones, and rolls unfinished issues from ended cycles into the current one.
// It is safe to run at any time and from several servers at once.
func keepTeamCycles(ctx context.Context, q *dbgen.Queries, team dbgen.Team, now time.Time) error {
	if team.CycleWeeks <= 0 {
		return nil
	}
	if err := q.LockTeamCycles(ctx, team.ID.String()); err != nil {
		return err
	}
	today := dateOf(now)
	length := time.Duration(team.CycleWeeks) * 7 * 24 * time.Hour

	cycles, err := q.ListCyclesForTeam(ctx, team.ID)
	if err != nil {
		return err
	}
	// Cycles run back to back; after a gap (cycles were off for a while) the
	// next one starts with the current week.
	for range 20 {
		upcoming := 0
		for _, c := range cycles {
			if c.StartsAt.After(today) {
				upcoming++
			}
		}
		if currentCycle(cycles, today) != nil && upcoming >= upcomingCycles {
			break
		}
		start, number := mondayOf(today), int32(1)
		if n := len(cycles); n > 0 {
			number = cycles[n-1].Number + 1
			if next := cycles[n-1].EndsAt.AddDate(0, 0, 1); next.After(start) {
				start = next
			}
		}
		cycle, err := q.CreateCycle(ctx, dbgen.CreateCycleParams{
			ID:          newID(),
			WorkspaceID: team.WorkspaceID,
			TeamID:      team.ID,
			Number:      number,
			StartsAt:    start,
			EndsAt:      start.Add(length).AddDate(0, 0, -1),
		})
		if err != nil {
			return err
		}
		if _, err := record(ctx, q, team.WorkspaceID, api.SyncModelCycle, cycle.ID, syncer.Insert, cycleRecord(cycle), &team, nil); err != nil {
			return err
		}
		cycles = append(cycles, cycle)
	}

	current := currentCycle(cycles, today)
	if current == nil {
		return nil
	}
	var ended []uuid.UUID
	for _, c := range cycles {
		if c.EndsAt.Before(today) {
			ended = append(ended, c.ID)
		}
	}
	if len(ended) == 0 {
		return nil
	}
	leftovers, err := q.ListOpenIssuesInCycles(ctx, ended)
	if err != nil {
		return err
	}
	for _, before := range leftovers {
		issue, err := q.SetIssueCycle(ctx, dbgen.SetIssueCycleParams{ID: before.ID, CycleID: &current.ID})
		if err != nil {
			return err
		}
		if _, err := recordIssue(ctx, q, team, issue, syncer.Update); err != nil {
			return err
		}
		// No actor: the system moved it.
		if err := logActivities(ctx, q, team, issue, nil, issueChanges(before, issue)); err != nil {
			return err
		}
	}
	return nil
}

// currentCycle returns the cycle that contains today, if there is one.
func currentCycle(cycles []dbgen.Cycle, today time.Time) *dbgen.Cycle {
	for i := range cycles {
		if !cycles[i].StartsAt.After(today) && !cycles[i].EndsAt.Before(today) {
			return &cycles[i]
		}
	}
	return nil
}

// dateOf returns the UTC calendar day of t, at midnight.
func dateOf(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// mondayOf returns the Monday of the week containing day.
func mondayOf(day time.Time) time.Time {
	offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
	return day.AddDate(0, 0, -offset)
}
