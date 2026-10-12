-- +goose Up
-- Kept only when the server has a SECRETS_KEY, and encrypted with it.

-- An import's request with its credentials, for as long as it runs, so that
-- a server that restarts can carry on with it. resumes counts how often
-- that has happened.
ALTER TABLE imports ADD COLUMN secret bytea;
ALTER TABLE imports ADD COLUMN resumes integer NOT NULL DEFAULT 0;

-- A tracker an admin has connected their account at, through its sign-in
-- page, so that importing from it takes no token. It is theirs alone: what
-- it can read is what they can read there.
CREATE TABLE import_connections (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider     text NOT NULL CHECK (provider IN ('github', 'linear', 'jira')),
    -- The account or site, in words: "octocat", "acme.atlassian.net".
    label        text NOT NULL DEFAULT '',
    -- The tokens, encrypted.
    secret       bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id, provider)
);

-- +goose Down
DROP TABLE import_connections;
ALTER TABLE imports DROP COLUMN resumes;
ALTER TABLE imports DROP COLUMN secret;
