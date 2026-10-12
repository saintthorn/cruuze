// Command seed fills a team with made-up issues that have a believable
// history, for trying the app at size and for demos of reports and
// forecasts. It writes straight to the database:
//
//	DATABASE_URL=... go run ./cmd/seed -workspace acme -team ENG -issues 50000
//
// Seeded issues don't go through the sync log, so the workspace's sync ID is
// bumped to make every client load a fresh copy.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

var (
	verbs = []string{"Fix", "Add", "Remove", "Refactor", "Investigate", "Speed up", "Document", "Test", "Redesign", "Migrate", "Handle", "Support"}
	nouns = []string{"login redirect", "invoice totals", "search index", "webhook retries", "CSV import", "session expiry", "avatar upload",
		"rate limiter", "onboarding flow", "audit log", "billing page", "email digest", "cycle rollover", "label picker", "keyboard shortcuts",
		"dark mode contrast", "pagination", "error messages", "API keys page", "team settings", "sync reconnect", "attachment preview"}
	details = []string{"on mobile", "for guests", "when offline", "in Safari", "after a restart", "for large workspaces", "with empty input",
		"under load", "for archived issues", "in the sidebar", "on first run", ""}
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	// One at a time: Load gives up at the first file that is missing.
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")
	workspaceKey := flag.String("workspace", "", "URL key of the workspace")
	teamKey := flag.String("team", "", "key of the team to fill")
	count := flag.Int("issues", 1000, "how many issues to create")
	days := flag.Int("days", 180, "how far back their history reaches")
	flag.Parse()
	if *workspaceKey == "" || *teamKey == "" || *count <= 0 || *days <= 0 {
		flag.Usage()
		return fmt.Errorf("-workspace and -team are required")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var workspaceID, teamID uuid.UUID
	var nextNumber int32
	err = tx.QueryRow(ctx, `
		SELECT w.id, t.id, t.next_issue_number
		FROM workspaces w JOIN teams t ON t.workspace_id = w.id
		WHERE w.url_key = $1 AND t.key = $2
		FOR UPDATE OF t`, *workspaceKey, *teamKey).Scan(&workspaceID, &teamID, &nextNumber)
	if err != nil {
		return fmt.Errorf("find team %s in workspace %s: %w", *teamKey, *workspaceKey, err)
	}

	states := map[string]uuid.UUID{}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (type) type, id FROM workflow_states WHERE team_id = $1 ORDER BY type, position`, teamID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind string
		var id uuid.UUID
		if err := rows.Scan(&kind, &id); err != nil {
			return err
		}
		states[kind] = id
	}
	for _, kind := range []string{"backlog", "started", "completed"} {
		if _, ok := states[kind]; !ok {
			return fmt.Errorf("the team has no %s status", kind)
		}
	}
	// A team without these kinds gets the nearest thing.
	if _, ok := states["unstarted"]; !ok {
		states["unstarted"] = states["backlog"]
	}
	if _, ok := states["canceled"]; !ok {
		states["canceled"] = states["completed"]
	}

	members, err := ids(ctx, tx, `SELECT user_id FROM workspace_members WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return err
	}
	labels, err := ids(ctx, tx, `SELECT id FROM labels WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return err
	}
	var topOrder float64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(min(sort_order), 0) FROM issues WHERE team_id = $1`, teamID).Scan(&topOrder); err != nil {
		return err
	}

	now := time.Now().UTC()
	span := time.Duration(*days) * 24 * time.Hour
	newIDs := make([]uuid.UUID, 0, *count)
	records := make([][]any, 0, *count)
	for n := range *count {
		// More issues recently than long ago, as in a team that is growing.
		age := time.Duration(math.Pow(rand.Float64(), 1.6) * float64(span))
		created := now.Add(-age)

		kind := pick(map[string]float64{"backlog": 0.24, "unstarted": 0.10, "started": 0.08, "completed": 0.52, "canceled": 0.06})
		var started, completed *time.Time
		if kind == "started" || kind == "completed" {
			at := created.Add(time.Duration(rand.ExpFloat64() * float64(4*24*time.Hour)))
			if at.After(now) {
				at = now
			}
			started = &at
		}
		if kind == "completed" || kind == "canceled" {
			from := created
			if started != nil {
				from = *started
			}
			// Cycle times are skewed: most things take days, a few take weeks.
			at := from.Add(time.Duration(math.Exp(rand.NormFloat64()*0.9+1.0) * float64(24*time.Hour)))
			if at.After(now) {
				// Not finished yet after all.
				kind = "backlog"
				if started != nil {
					kind = "started"
				}
			} else {
				completed = &at
			}
		}
		updated := created
		if started != nil {
			updated = *started
		}
		if completed != nil {
			updated = *completed
		}

		id := uuid.Must(uuid.NewV7())
		newIDs = append(newIDs, id)
		var assignee *uuid.UUID
		if len(members) > 0 && rand.Float64() < 0.7 {
			assignee = &members[rand.IntN(len(members))]
		}
		issueLabels := []uuid.UUID{}
		if len(labels) > 0 && rand.Float64() < 0.4 {
			issueLabels = append(issueLabels, labels[rand.IntN(len(labels))])
		}
		title := verbs[rand.IntN(len(verbs))] + " " + nouns[rand.IntN(len(nouns))]
		if detail := details[rand.IntN(len(details))]; detail != "" {
			title += " " + detail
		}
		records = append(records, []any{
			id, workspaceID, teamID, nextNumber + int32(n), title, "", states[kind], int16(rand.IntN(5)), assignee,
			topOrder - float64(*count-n), issueLabels, []uuid.UUID{}, created, updated, started, completed,
		})
	}

	columns := []string{"id", "workspace_id", "team_id", "number", "title", "description", "state_id", "priority", "assignee_id",
		"sort_order", "label_ids", "subscriber_ids", "created_at", "updated_at", "started_at", "completed_at"}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"issues"}, columns, pgx.CopyFromRows(records)); err != nil {
		return fmt.Errorf("insert issues: %w", err)
	}

	// The insert trigger logged every issue as entering its state just now.
	// Replace that with the history the timestamps describe: waiting, then
	// (for some) in progress, then (for some) closed.
	steps := []string{
		`DELETE FROM issue_state_history WHERE issue_id = ANY($1)`,
		`INSERT INTO issue_state_history (workspace_id, team_id, issue_id, state_id, state_type, entered_at, left_at)
		 SELECT i.workspace_id, i.team_id, i.id, w.id, w.type, i.created_at, COALESCE(i.started_at, i.completed_at)
		 FROM issues i
		 JOIN workflow_states s ON s.id = i.state_id
		 JOIN workflow_states w ON w.id = CASE WHEN s.type IN ('backlog', 'unstarted') THEN s.id ELSE $2::uuid END
		 WHERE i.id = ANY($1)`,
		`INSERT INTO issue_state_history (workspace_id, team_id, issue_id, state_id, state_type, entered_at, left_at)
		 SELECT i.workspace_id, i.team_id, i.id, $2::uuid, 'started', i.started_at, i.completed_at
		 FROM issues i WHERE i.id = ANY($1) AND i.started_at IS NOT NULL`,
		`INSERT INTO issue_state_history (workspace_id, team_id, issue_id, state_id, state_type, entered_at)
		 SELECT i.workspace_id, i.team_id, i.id, s.id, s.type, i.completed_at
		 FROM issues i JOIN workflow_states s ON s.id = i.state_id
		 WHERE i.id = ANY($1) AND i.completed_at IS NOT NULL`,
	}
	args := [][]any{{newIDs}, {newIDs, states["backlog"]}, {newIDs, states["started"]}, {newIDs}}
	for i, step := range steps {
		if _, err := tx.Exec(ctx, step, args[i]...); err != nil {
			return fmt.Errorf("rebuild history (step %d): %w", i+1, err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE teams SET next_issue_number = next_issue_number + $2 WHERE id = $1`, teamID, *count); err != nil {
		return err
	}
	// A sync ID with no log entry behind it: clients see a gap they can't
	// replay across and bootstrap again, which is how they get these issues.
	if _, err := tx.Exec(ctx, `UPDATE workspaces SET last_sync_id = last_sync_id + 1 WHERE id = $1`, workspaceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("created %d issues in %s/%s\n", *count, *workspaceKey, *teamKey)
	return nil
}

func ids(ctx context.Context, tx pgx.Tx, query string, arg any) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// pick chooses a key with probability proportional to its weight.
func pick(weights map[string]float64) string {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	r := rand.Float64() * total
	last := ""
	for key, w := range weights {
		last = key
		if r -= w; r < 0 {
			return key
		}
	}
	return last
}
