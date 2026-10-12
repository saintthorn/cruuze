-- +goose Up
CREATE TABLE labels (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text NOT NULL,
    color        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
-- Label names are unique within a workspace, ignoring case.
CREATE UNIQUE INDEX labels_workspace_name_idx ON labels (workspace_id, lower(name));

ALTER TABLE issues
    -- An array rather than a join table: issue rows are synced to clients as
    -- whole snapshots, and this keeps a snapshot in one row. Deleting a label
    -- removes it from these arrays (see DeleteLabel).
    ADD COLUMN label_ids uuid[] NOT NULL DEFAULT '{}',
    ADD COLUMN estimate smallint CHECK (estimate BETWEEN 0 AND 100),
    ADD COLUMN due_date date;
CREATE INDEX issues_label_ids_idx ON issues USING gin (label_ids);

-- +goose Down
ALTER TABLE issues DROP COLUMN due_date, DROP COLUMN estimate, DROP COLUMN label_ids;
DROP TABLE labels;
