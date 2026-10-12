-- +goose Up

-- Unstarted issues nobody has touched for this many days are canceled; 0 never.
ALTER TABLE teams ADD COLUMN auto_close_days integer NOT NULL DEFAULT 0;
CREATE INDEX issues_untouched_idx ON issues (team_id, updated_at) WHERE archived_at IS NULL AND completed_at IS NULL;

-- An install from before there were instance admins has none. Its admin is
-- who it would have been: the first person who signed in.
UPDATE users SET instance_admin = true
WHERE id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM users WHERE instance_admin);

-- +goose Down
DROP INDEX issues_untouched_idx;
ALTER TABLE teams DROP COLUMN auto_close_days;
