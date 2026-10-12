-- +goose Up

-- A workspace can open a public page where its customers ask for things.
ALTER TABLE workspaces
    ADD COLUMN portal_enabled      boolean NOT NULL DEFAULT false,
    -- Whether requests marked public are listed there for others to vote on.
    ADD COLUMN portal_public_board boolean NOT NULL DEFAULT false,
    ADD COLUMN portal_intro        text NOT NULL DEFAULT '';

-- Something a customer asked for, from the portal or noted down by the team.
CREATE TABLE customer_requests (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    requester_email citext NOT NULL,
    -- The portal user who sent it; NULL when the team wrote it down for them.
    requester_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    title           text NOT NULL,
    body            text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'new'
                    CHECK (status IN ('new', 'reviewing', 'planned', 'shipped', 'declined')),
    -- Shown on the public board, if the workspace has one.
    public          boolean NOT NULL DEFAULT false,
    -- The team's answer, shown to the requester.
    response        text NOT NULL DEFAULT '',
    -- The issue that would deliver it.
    issue_id        uuid REFERENCES issues (id) ON DELETE SET NULL,
    vote_count      integer NOT NULL DEFAULT 0,
    shipped_at      timestamptz,
    -- When the requester was told it shipped; they are told once.
    notified_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_requests_workspace_idx ON customer_requests (workspace_id, created_at DESC);
CREATE INDEX customer_requests_issue_idx ON customer_requests (issue_id) WHERE issue_id IS NOT NULL;
CREATE INDEX customer_requests_requester_idx ON customer_requests (workspace_id, requester_email);
CREATE INDEX customer_requests_to_notify_idx ON customer_requests (shipped_at) WHERE status = 'shipped' AND notified_at IS NULL;

CREATE TABLE customer_request_votes (
    request_id uuid NOT NULL REFERENCES customer_requests (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, user_id)
);

-- Changes that guests don't get: customers' requests carry their addresses.
ALTER TABLE sync_actions ADD COLUMN members_only boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE sync_actions DROP COLUMN members_only;
DROP TABLE customer_request_votes;
DROP TABLE customer_requests;
ALTER TABLE workspaces DROP COLUMN portal_intro, DROP COLUMN portal_public_board, DROP COLUMN portal_enabled;
