-- +goose Up

-- What a team's issues do by themselves. On by default: the point is that
-- nobody has to remember to update a ticket.
ALTER TABLE teams
    -- A parent issue is completed when its last open sub-issue is closed.
    ADD COLUMN auto_close_parent boolean NOT NULL DEFAULT true,
    -- Whoever starts an unassigned issue becomes its assignee.
    ADD COLUMN auto_assign boolean NOT NULL DEFAULT true,
    -- An issue that is started joins the team's current cycle.
    ADD COLUMN auto_cycle boolean NOT NULL DEFAULT true,
    -- Where an issue goes when its pull request asks for review; NULL leaves it be.
    ADD COLUMN review_state_id uuid REFERENCES workflow_states (id) ON DELETE SET NULL;

-- Lets GitHub activity be credited to the person who did it.
ALTER TABLE users ADD COLUMN github_login text NOT NULL DEFAULT '';
CREATE INDEX users_github_login_idx ON users (lower(github_login)) WHERE github_login <> '';

-- +goose Down
DROP INDEX users_github_login_idx;
ALTER TABLE users DROP COLUMN github_login;
ALTER TABLE teams
    DROP COLUMN review_state_id,
    DROP COLUMN auto_cycle,
    DROP COLUMN auto_assign,
    DROP COLUMN auto_close_parent;
