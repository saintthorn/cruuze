-- +goose Up

-- The bootstrap and most lists read a team's unarchived issues.
CREATE INDEX issues_team_active_idx ON issues (team_id) WHERE archived_at IS NULL;

-- Closed issues are archived by themselves after this many days, which is
-- what keeps the set every client holds from growing forever. 0 turns it off.
ALTER TABLE teams ADD COLUMN auto_archive_days integer NOT NULL DEFAULT 90
    CHECK (auto_archive_days BETWEEN 0 AND 3650);
CREATE INDEX issues_to_archive_idx ON issues (team_id, completed_at)
    WHERE archived_at IS NULL AND completed_at IS NOT NULL;

-- +goose Down
DROP INDEX issues_to_archive_idx;
ALTER TABLE teams DROP COLUMN auto_archive_days;
DROP INDEX issues_team_active_idx;
