-- +goose Up

-- People who are notified about an issue. An array for the same reason as
-- label_ids: the issue is synced as one snapshot.
ALTER TABLE issues ADD COLUMN subscriber_ids uuid[] NOT NULL DEFAULT '{}';
UPDATE issues
SET subscriber_ids = ARRAY(
    SELECT DISTINCT u FROM unnest(ARRAY[creator_id, assignee_id]) AS u WHERE u IS NOT NULL
);

-- Sync actions can now be private to one user (their notifications).
ALTER TABLE sync_actions ADD COLUMN user_id uuid;

CREATE TABLE comments (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    issue_id     uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    user_id      uuid REFERENCES users (id) ON DELETE SET NULL,
    -- Markdown.
    body         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    edited_at    timestamptz
);
CREATE INDEX comments_issue_id_idx ON comments (issue_id);

CREATE TABLE comment_reactions (
    comment_id uuid NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    emoji      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, user_id, emoji)
);

-- One row per change to an issue, shown as its activity feed.
CREATE TABLE issue_activities (
    id                uuid PRIMARY KEY,
    workspace_id      uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    issue_id          uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    actor_id          uuid REFERENCES users (id) ON DELETE SET NULL,
    type              text NOT NULL,
    -- The old and new value as text (an ID, a number, a date); NULL for none.
    from_value        text,
    to_value          text,
    added_label_ids   uuid[] NOT NULL DEFAULT '{}',
    removed_label_ids uuid[] NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX issue_activities_issue_id_idx ON issue_activities (issue_id);

CREATE TABLE notifications (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- The recipient.
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issue_id     uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    actor_id     uuid REFERENCES users (id) ON DELETE SET NULL,
    comment_id   uuid REFERENCES comments (id) ON DELETE SET NULL,
    type         text NOT NULL CHECK (type IN ('assigned', 'mentioned', 'commented', 'status')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    read_at      timestamptz
);
CREATE INDEX notifications_user_idx ON notifications (user_id, workspace_id, created_at DESC);

CREATE TABLE attachments (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    issue_id     uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    uploader_id  uuid REFERENCES users (id) ON DELETE SET NULL,
    filename     text NOT NULL,
    content_type text NOT NULL,
    size         bigint NOT NULL,
    -- Where the bytes live in object storage.
    storage_key  text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_issue_id_idx ON attachments (issue_id);

-- +goose Down
DROP TABLE attachments;
DROP TABLE notifications;
DROP TABLE issue_activities;
DROP TABLE comment_reactions;
DROP TABLE comments;
ALTER TABLE sync_actions DROP COLUMN user_id;
ALTER TABLE issues DROP COLUMN subscriber_ids;
