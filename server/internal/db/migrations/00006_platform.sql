-- +goose Up

-- Full-text search over issue titles, descriptions and comments. The search
-- query must use these exact expressions for the indexes to apply.
CREATE INDEX issues_search_idx ON issues USING gin (
    (setweight(to_tsvector('english', title), 'A') || setweight(to_tsvector('english', description), 'B'))
);
CREATE INDEX comments_search_idx ON comments USING gin (to_tsvector('english', body));

-- A key lets a script act as its owner within one workspace.
CREATE TABLE api_keys (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    -- The end of the key, to tell keys apart once the key itself is gone.
    hint         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);
CREATE INDEX api_keys_owner_idx ON api_keys (workspace_id, user_id);

CREATE TABLE webhooks (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    label        text NOT NULL,
    url          text NOT NULL,
    -- Signs each delivery (HMAC-SHA256), so it has to be stored in the clear.
    secret       text NOT NULL,
    -- NULL covers every public team plus workspace-wide records; otherwise
    -- only that team's records are sent.
    team_id      uuid REFERENCES teams (id) ON DELETE CASCADE,
    -- The sync models this webhook is sent.
    models       text[] NOT NULL,
    enabled      boolean NOT NULL DEFAULT true,
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhooks_workspace_id_idx ON webhooks (workspace_id);

-- An outbox: rows are written in the transaction that makes the change and
-- sent afterwards, with retries.
CREATE TABLE webhook_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id      uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    model           text NOT NULL,
    action          text NOT NULL,
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    response_status integer,
    error           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz
);
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_webhook_idx ON webhook_deliveries (webhook_id, created_at DESC);

-- GitHub sends pull request events to a per-workspace URL, signed with this secret.
CREATE TABLE github_integrations (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    secret       text NOT NULL,
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- A pull request linked to an issue. One pull request can link several issues.
CREATE TABLE pull_requests (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    issue_id     uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    url          text NOT NULL,
    repo         text NOT NULL,
    number       integer NOT NULL,
    title        text NOT NULL,
    branch       text NOT NULL,
    author       text NOT NULL,
    status       text NOT NULL CHECK (status IN ('draft', 'open', 'merged', 'closed')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issue_id, url)
);
CREATE INDEX pull_requests_url_idx ON pull_requests (workspace_id, url);

-- Unread notifications are emailed after a delay; emailed_at marks the ones
-- already handled. Existing notifications are not emailed retroactively.
ALTER TABLE notifications ADD COLUMN emailed_at timestamptz;
UPDATE notifications SET emailed_at = now();
CREATE INDEX notifications_to_email_idx ON notifications (created_at) WHERE emailed_at IS NULL AND read_at IS NULL;

ALTER TABLE users ADD COLUMN email_notifications boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE users DROP COLUMN email_notifications;
DROP INDEX notifications_to_email_idx;
ALTER TABLE notifications DROP COLUMN emailed_at;
DROP TABLE pull_requests;
DROP TABLE github_integrations;
DROP TABLE webhook_deliveries;
DROP TABLE webhooks;
DROP TABLE api_keys;
DROP INDEX comments_search_idx;
DROP INDEX issues_search_idx;
