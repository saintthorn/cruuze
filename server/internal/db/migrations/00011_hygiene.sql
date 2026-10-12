-- +goose Up

-- An open issue nobody has touched for this many days is flagged for
-- cleanup. 0 turns the flagging off.
ALTER TABLE teams ADD COLUMN stale_days integer NOT NULL DEFAULT 60
    CHECK (stale_days BETWEEN 0 AND 3650);

CREATE INDEX issues_archived_idx ON issues (team_id, archived_at DESC) WHERE archived_at IS NOT NULL;

-- +goose Down
DROP INDEX issues_archived_idx;
ALTER TABLE teams DROP COLUMN stale_days;
