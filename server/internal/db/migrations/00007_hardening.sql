-- +goose Up

-- Marks a change that alters who can see a team (it went private or public).
-- Everyone who can see the workspace but isn't a member of team_id has a copy
-- that is now wrong and must bootstrap again.
ALTER TABLE sync_actions ADD COLUMN resets boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE sync_actions DROP COLUMN resets;
