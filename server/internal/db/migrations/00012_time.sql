-- +goose Up

-- Time someone spent on an issue: a finished stretch, or a timer that is
-- still running (ended_at is NULL). A person sees their own entries; what
-- everyone sees is the total on the issue.
CREATE TABLE time_entries (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    issue_id     uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    started_at   timestamptz NOT NULL,
    ended_at     timestamptz,
    -- How long it lasted; 0 while the timer runs.
    seconds      integer NOT NULL DEFAULT 0 CHECK (seconds >= 0),
    note         text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX time_entries_issue_idx ON time_entries (issue_id);
CREATE INDEX time_entries_user_idx ON time_entries (workspace_id, user_id, started_at DESC);
-- One timer at a time per person.
CREATE UNIQUE INDEX time_entries_running_idx ON time_entries (user_id) WHERE ended_at IS NULL;

-- Everyone's finished time on the issue, kept in step with time_entries.
ALTER TABLE issues ADD COLUMN logged_seconds integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE issues DROP COLUMN logged_seconds;
DROP TABLE time_entries;
