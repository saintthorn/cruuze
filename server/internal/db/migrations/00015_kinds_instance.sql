-- +goose Up

-- What sort of team it is: decides the workflow it starts with and which
-- pages the app offers it.
ALTER TABLE teams ADD COLUMN kind text NOT NULL DEFAULT 'engineering'
    CHECK (kind IN ('engineering', 'support', 'marketing', 'design', 'general'));

-- Whoever runs the server. The first person to sign in on a new install.
ALTER TABLE users ADD COLUMN instance_admin boolean NOT NULL DEFAULT false;

-- Settings for the whole install; one row.
CREATE TABLE instance_settings (
    only_row           boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    -- Who may make an account: anyone, people who were invited to a
    -- workspace, or (also) addresses at the listed domains.
    signup             text NOT NULL DEFAULT 'open' CHECK (signup IN ('open', 'invited', 'domains')),
    signup_domains     text[] NOT NULL DEFAULT '{}',
    workspace_creation text NOT NULL DEFAULT 'anyone' CHECK (workspace_creation IN ('anyone', 'admins')),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
INSERT INTO instance_settings DEFAULT VALUES;

-- +goose Down
DROP TABLE instance_settings;
ALTER TABLE users DROP COLUMN instance_admin;
ALTER TABLE teams DROP COLUMN kind;
