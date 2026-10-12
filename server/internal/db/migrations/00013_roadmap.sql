-- +goose Up

-- An initiative is a goal that several projects serve.
CREATE TABLE initiatives (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    color        text NOT NULL,
    target_date  date,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX initiatives_workspace_id_idx ON initiatives (workspace_id);

-- Deleting an initiative leaves its projects in place.
ALTER TABLE projects ADD COLUMN initiative_id uuid REFERENCES initiatives (id) ON DELETE SET NULL;
CREATE INDEX projects_initiative_id_idx ON projects (initiative_id);

-- "project can't finish until depends_on has".
CREATE TABLE project_dependencies (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    project_id    uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    depends_on_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, depends_on_id),
    CHECK (project_id <> depends_on_id)
);
CREATE INDEX project_dependencies_workspace_idx ON project_dependencies (workspace_id);
CREATE INDEX project_dependencies_depends_on_idx ON project_dependencies (depends_on_id);

-- A dated note on how a project is going. The newest one is the project's health.
CREATE TABLE project_updates (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    project_id   uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id      uuid REFERENCES users (id) ON DELETE SET NULL,
    health       text NOT NULL CHECK (health IN ('onTrack', 'atRisk', 'offTrack')),
    body         text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX project_updates_project_idx ON project_updates (project_id, created_at DESC);
CREATE INDEX project_updates_workspace_idx ON project_updates (workspace_id);

-- +goose Down
DROP TABLE project_updates;
DROP TABLE project_dependencies;
ALTER TABLE projects DROP COLUMN initiative_id;
DROP TABLE initiatives;
