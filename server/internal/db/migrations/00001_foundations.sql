-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id         uuid PRIMARY KEY,
    email      citext NOT NULL UNIQUE,
    name       text NOT NULL DEFAULT '',
    avatar_url text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Third-party identities linked to a user.
CREATE TABLE oauth_accounts (
    provider         text NOT NULL,
    provider_user_id text NOT NULL,
    user_id          uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_user_id)
);
CREATE INDEX oauth_accounts_user_id_idx ON oauth_accounts (user_id);

-- Single-use magic-link tokens. Only the SHA-256 of the token is stored.
CREATE TABLE login_tokens (
    id          uuid PRIMARY KEY,
    email       citext NOT NULL,
    token_hash  bytea NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_tokens_email_created_at_idx ON login_tokens (email, created_at);

-- Browser sessions. Only the SHA-256 of the cookie value is stored.
CREATE TABLE sessions (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    user_agent   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

CREATE TABLE workspaces (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    url_key    text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspace_members (
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         text NOT NULL CHECK (role IN ('admin', 'member', 'guest')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX workspace_members_user_id_idx ON workspace_members (user_id);

-- Pending invites; accepted when a user with that email signs in.
CREATE TABLE workspace_invites (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    email        citext NOT NULL,
    role         text NOT NULL CHECK (role IN ('admin', 'member', 'guest')),
    invited_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, email)
);
CREATE INDEX workspace_invites_email_idx ON workspace_invites (email);

CREATE TABLE teams (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text NOT NULL,
    key          text NOT NULL,
    private      boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, key)
);

CREATE TABLE team_members (
    team_id    uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user_id_idx ON team_members (user_id);

-- +goose Down
DROP TABLE team_members;
DROP TABLE teams;
DROP TABLE workspace_invites;
DROP TABLE workspace_members;
DROP TABLE workspaces;
DROP TABLE sessions;
DROP TABLE login_tokens;
DROP TABLE oauth_accounts;
DROP TABLE users;
