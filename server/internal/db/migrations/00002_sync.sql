-- +goose Up

-- Per-workspace sync counter. Mutations increment it inside their transaction;
-- the row lock this takes serializes writers within a workspace, so sync IDs
-- are gapless and become visible in order.
ALTER TABLE workspaces ADD COLUMN last_sync_id bigint NOT NULL DEFAULT 0;

ALTER TABLE teams ADD COLUMN next_issue_number integer NOT NULL DEFAULT 1;

CREATE TABLE workflow_states (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    team_id      uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    name         text NOT NULL,
    type         text NOT NULL CHECK (type IN ('backlog', 'unstarted', 'started', 'completed', 'canceled')),
    color        text NOT NULL,
    position     double precision NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workflow_states_team_id_idx ON workflow_states (team_id);
CREATE INDEX workflow_states_workspace_id_idx ON workflow_states (workspace_id);

-- Every existing team gets the default workflow.
INSERT INTO workflow_states (id, workspace_id, team_id, name, type, color, position)
SELECT gen_random_uuid(), t.workspace_id, t.id, s.name, s.type, s.color, s.position
FROM teams t
CROSS JOIN (VALUES
    ('Backlog',     'backlog',   '#95a2b3', 0),
    ('Todo',        'unstarted', '#e2e2e2', 1),
    ('In Progress', 'started',   '#f2c94c', 2),
    ('Done',        'completed', '#5e6ad2', 3),
    ('Canceled',    'canceled',  '#95a2b3', 4)
) AS s (name, type, color, position);

CREATE TABLE issues (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    team_id      uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    number       integer NOT NULL,
    title        text NOT NULL,
    description  text NOT NULL DEFAULT '',
    state_id     uuid NOT NULL REFERENCES workflow_states (id),
    priority     smallint NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 4),
    assignee_id  uuid REFERENCES users (id) ON DELETE SET NULL,
    creator_id   uuid REFERENCES users (id) ON DELETE SET NULL,
    sort_order   double precision NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    archived_at  timestamptz,
    UNIQUE (team_id, number)
);
CREATE INDEX issues_workspace_id_idx ON issues (workspace_id) WHERE archived_at IS NULL;
CREATE INDEX issues_state_id_idx ON issues (state_id);

-- Append-only log of every change to a synced model. Clients hold the last
-- sync_id they applied and replay everything after it.
CREATE TABLE sync_actions (
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    sync_id      bigint NOT NULL,
    model        text NOT NULL,
    model_id     uuid NOT NULL,
    action       text NOT NULL CHECK (action IN ('I', 'U', 'D')),
    -- Full snapshot of the record after the change (before it, for deletes).
    data         jsonb NOT NULL,
    -- Visibility scope: NULL means everyone in the workspace. Not a foreign
    -- key, because the log outlives the team.
    team_id      uuid,
    -- Whether that team was private when the action was recorded.
    team_private boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, sync_id)
);
CREATE INDEX sync_actions_created_at_idx ON sync_actions (created_at);

-- +goose Down
DROP TABLE sync_actions;
DROP TABLE issues;
DROP TABLE workflow_states;
ALTER TABLE teams DROP COLUMN next_issue_number;
ALTER TABLE workspaces DROP COLUMN last_sync_id;
