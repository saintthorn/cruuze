package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRemoveEstimatesMigration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// Temporary tables isolate the upgrade fixture from other tests and data.
	_, err = conn.Exec(ctx, `
		CREATE TEMP TABLE issues (id int PRIMARY KEY, title text, estimate smallint);
		CREATE TEMP TABLE issue_activities (id int PRIMARY KEY, type text, to_value text);
		CREATE TEMP TABLE sync_actions (sync_id int PRIMARY KEY, model text, data jsonb);
		INSERT INTO issues VALUES (1, 'Keep the issue', 8), (2, 'No estimate', NULL);
		INSERT INTO issue_activities VALUES (1, 'estimate', '8'), (2, 'title', 'Keep the issue');
		INSERT INTO sync_actions VALUES
			(1, 'Issue', '{"id":1,"title":"Keep the issue","estimate":8}'),
			(2, 'Activity', '{"id":1,"type":"estimate","to":"8"}'),
			(3, 'Activity', '{"id":2,"type":"title","to":"Keep the issue"}'),
			(4, 'Other', '{"estimate":3}');
	`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrationsFS.ReadFile("migrations/00019_remove_estimates.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(migration), "-- +goose Down")[0]
	if _, err := conn.Exec(ctx, up); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	var columnGone, issuesKept, activityKept, snapshotsClean, unrelatedKept bool
	err = conn.QueryRow(ctx, `SELECT
		NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'pg_temp.issues'::regclass AND attname = 'estimate' AND NOT attisdropped),
		(SELECT count(*) = 2 AND bool_or(title = 'Keep the issue') FROM issues),
		(SELECT count(*) = 1 AND bool_and(type = 'title' AND to_value = 'Keep the issue') FROM issue_activities),
		(SELECT data = '{"id":1,"title":"Keep the issue"}'::jsonb FROM sync_actions WHERE sync_id = 1)
			AND NOT EXISTS (SELECT 1 FROM sync_actions WHERE sync_id = 2),
		(SELECT count(*) = 2 FROM sync_actions WHERE sync_id IN (3, 4))
			AND (SELECT data = '{"estimate":3}'::jsonb FROM sync_actions WHERE sync_id = 4)
	`).Scan(&columnGone, &issuesKept, &activityKept, &snapshotsClean, &unrelatedKept)
	if err != nil {
		t.Fatal(err)
	}
	if !columnGone || !issuesKept || !activityKept || !snapshotsClean || !unrelatedKept {
		t.Fatalf("migration checks: column gone=%v, issues kept=%v, activity kept=%v, snapshots clean=%v, unrelated kept=%v", columnGone, issuesKept, activityKept, snapshotsClean, unrelatedKept)
	}
}
