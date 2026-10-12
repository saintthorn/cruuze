-- +goose Up

-- One run of an importer: bringing a team's work in from another tracker.
CREATE TABLE imports (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- Where from: 'github', 'linear', 'jira', 'bundle', 'csv'.
    source       text NOT NULL,
    -- What, in words: "acme/widgets", "acme.atlassian.net".
    label        text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done', 'failed', 'undone')),
    -- What it is doing right now, for whoever is watching.
    stage        text NOT NULL DEFAULT '',
    -- How many of each thing it created or updated: {"issues": 120, ...}.
    counts       jsonb NOT NULL DEFAULT '{}',
    -- Things the person should know that didn't stop it.
    warnings     text[] NOT NULL DEFAULT '{}',
    error        text NOT NULL DEFAULT '',
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- Touched as the import makes progress. A running import that has not
    -- been heard from for a while died with its server.
    heartbeat_at timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);
CREATE INDEX imports_workspace_idx ON imports (workspace_id, created_at DESC);

-- Which record an imported thing became. This is what makes running an
-- import again update what is there instead of adding it twice, and what
-- undoing an import goes by.
CREATE TABLE import_items (
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- The place it came from: "github:acme/widgets". IDs are unique within it.
    origin       text NOT NULL,
    -- 'team', 'state', 'label', 'project', 'milestone', 'cycle', 'issue', 'comment', 'attachment'.
    kind         text NOT NULL,
    external_id  text NOT NULL,
    record_id    uuid NOT NULL,
    -- The import that created the record; NULL if it was matched to one
    -- that already existed (which an undo must leave alone).
    created_by   uuid REFERENCES imports (id) ON DELETE SET NULL,
    -- When the source last changed it, as of the last import.
    source_updated_at timestamptz,
    PRIMARY KEY (workspace_id, origin, kind, external_id)
);
CREATE INDEX import_items_created_by_idx ON import_items (created_by);
CREATE INDEX import_items_record_idx ON import_items (record_id);

-- +goose Down
DROP TABLE import_items;
DROP TABLE imports;
