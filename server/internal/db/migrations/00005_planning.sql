-- +goose Up

-- Triage is a holding state for issues nobody on the team has accepted yet.
ALTER TABLE workflow_states DROP CONSTRAINT workflow_states_type_check;
ALTER TABLE workflow_states ADD CONSTRAINT workflow_states_type_check
    CHECK (type IN ('triage', 'backlog', 'unstarted', 'started', 'completed', 'canceled'));

ALTER TABLE teams
    -- Length of the team's cycles in weeks; 0 turns cycles off.
    ADD COLUMN cycle_weeks smallint NOT NULL DEFAULT 0 CHECK (cycle_weeks BETWEEN 0 AND 8),
    ADD COLUMN triage_enabled boolean NOT NULL DEFAULT false;

CREATE TABLE projects (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'backlog'
                 CHECK (status IN ('backlog', 'planned', 'started', 'paused', 'completed', 'canceled')),
    lead_id      uuid REFERENCES users (id) ON DELETE SET NULL,
    start_date   date,
    target_date  date,
    color        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX projects_workspace_id_idx ON projects (workspace_id);

CREATE TABLE milestones (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    project_id   uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name         text NOT NULL,
    target_date  date,
    sort_order   double precision NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX milestones_project_id_idx ON milestones (project_id);

CREATE TABLE cycles (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    team_id      uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    number       integer NOT NULL,
    name         text NOT NULL DEFAULT '',
    -- Both days are part of the cycle.
    starts_at    date NOT NULL,
    ends_at      date NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, number)
);

-- Deleting a project, milestone, cycle or parent leaves its issues in place.
ALTER TABLE issues
    ADD COLUMN project_id   uuid REFERENCES projects (id) ON DELETE SET NULL,
    ADD COLUMN milestone_id uuid REFERENCES milestones (id) ON DELETE SET NULL,
    ADD COLUMN cycle_id     uuid REFERENCES cycles (id) ON DELETE SET NULL,
    ADD COLUMN parent_id    uuid REFERENCES issues (id) ON DELETE SET NULL;
CREATE INDEX issues_project_id_idx ON issues (project_id);
CREATE INDEX issues_cycle_id_idx ON issues (cycle_id);
CREATE INDEX issues_parent_id_idx ON issues (parent_id);

-- "issue blocks related", "issue duplicates related", "issue relates to related".
CREATE TABLE issue_relations (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    issue_id         uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    related_issue_id uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    type             text NOT NULL CHECK (type IN ('blocks', 'duplicate', 'related')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issue_id, related_issue_id, type),
    CHECK (issue_id <> related_issue_id)
);
CREATE INDEX issue_relations_related_idx ON issue_relations (related_issue_id);

CREATE TABLE issue_templates (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    team_id      uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    name         text NOT NULL,
    title        text NOT NULL DEFAULT '',
    description  text NOT NULL DEFAULT '',
    priority     smallint NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 4),
    label_ids    uuid[] NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- A saved combination of filters and display options.
CREATE TABLE views (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    owner_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL,
    -- Shared views are visible to the whole workspace; others only to their owner.
    shared       boolean NOT NULL DEFAULT false,
    -- Limits the view to one team's issues; NULL means every team.
    team_id      uuid REFERENCES teams (id) ON DELETE CASCADE,
    options      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE favorites (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('issue', 'project', 'view', 'cycle')),
    target_id    uuid NOT NULL,
    sort_order   double precision NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, kind, target_id)
);

-- +goose Down
DROP TABLE favorites;
DROP TABLE views;
DROP TABLE issue_templates;
DROP TABLE issue_relations;
ALTER TABLE issues DROP COLUMN parent_id, DROP COLUMN cycle_id, DROP COLUMN milestone_id, DROP COLUMN project_id;
DROP TABLE cycles;
DROP TABLE milestones;
DROP TABLE projects;
ALTER TABLE teams DROP COLUMN triage_enabled, DROP COLUMN cycle_weeks;
UPDATE workflow_states SET type = 'backlog' WHERE type = 'triage';
ALTER TABLE workflow_states DROP CONSTRAINT workflow_states_type_check;
ALTER TABLE workflow_states ADD CONSTRAINT workflow_states_type_check
    CHECK (type IN ('backlog', 'unstarted', 'started', 'completed', 'canceled'));
