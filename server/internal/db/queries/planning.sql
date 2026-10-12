-- Creates take a client-chosen ID. ON CONFLICT DO NOTHING makes a retried
-- create return no row instead of failing, so the handler can answer with the
-- record that is already there.

-- name: CreateProject :one
INSERT INTO projects (id, workspace_id, name, description, status, lead_id, start_date, target_date, color, initiative_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1;

-- name: UpdateProject :one
UPDATE projects
SET name        = COALESCE(sqlc.narg(name), name),
    description = COALESCE(sqlc.narg(description), description),
    status      = COALESCE(sqlc.narg(status), status),
    color       = COALESCE(sqlc.narg(color), color),
    lead_id     = CASE WHEN sqlc.arg(set_lead)::boolean THEN sqlc.narg(lead_id) ELSE lead_id END,
    start_date  = CASE WHEN sqlc.arg(set_start_date)::boolean THEN sqlc.narg(start_date) ELSE start_date END,
    target_date = CASE WHEN sqlc.arg(set_target_date)::boolean THEN sqlc.narg(target_date) ELSE target_date END,
    initiative_id = CASE WHEN sqlc.arg(set_initiative)::boolean THEN sqlc.narg(initiative_id) ELSE initiative_id END,
    updated_at  = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = $1;

-- name: ListProjects :many
SELECT * FROM projects WHERE workspace_id = $1 ORDER BY created_at;

-- name: CreateMilestone :one
INSERT INTO milestones (id, workspace_id, project_id, name, target_date, sort_order)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetMilestone :one
SELECT * FROM milestones WHERE id = $1;

-- name: UpdateMilestone :one
UPDATE milestones
SET name        = COALESCE(sqlc.narg(name), name),
    sort_order  = COALESCE(sqlc.narg(sort_order), sort_order),
    target_date = CASE WHEN sqlc.arg(set_target_date)::boolean THEN sqlc.narg(target_date) ELSE target_date END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteMilestone :exec
DELETE FROM milestones WHERE id = $1;

-- name: ListMilestones :many
SELECT * FROM milestones WHERE workspace_id = $1 ORDER BY sort_order;

-- name: MaxMilestoneSortOrder :one
SELECT COALESCE(max(sort_order), 0)::double precision FROM milestones WHERE project_id = $1;

-- name: CreateCycle :one
INSERT INTO cycles (id, workspace_id, team_id, number, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCycle :one
SELECT * FROM cycles WHERE id = $1;

-- name: ListCyclesForTeam :many
SELECT * FROM cycles WHERE team_id = $1 ORDER BY number;

-- name: ListCyclesForTeams :many
SELECT * FROM cycles WHERE team_id = ANY(sqlc.arg(team_ids)::uuid[]) ORDER BY team_id, number;

-- name: CreateIssueRelation :one
INSERT INTO issue_relations (id, workspace_id, issue_id, related_issue_id, type)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetIssueRelation :one
SELECT * FROM issue_relations WHERE id = $1;

-- name: DeleteIssueRelation :exec
DELETE FROM issue_relations WHERE id = $1;

-- name: ListRelationsForTeams :many
SELECT r.*
FROM issue_relations r
JOIN issues i ON i.id = r.issue_id
WHERE i.team_id = ANY(sqlc.arg(team_ids)::uuid[]) AND i.archived_at IS NULL;

-- name: CreateIssueTemplate :one
INSERT INTO issue_templates (id, workspace_id, team_id, name, title, description, priority, label_ids)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetIssueTemplate :one
SELECT * FROM issue_templates WHERE id = $1;

-- name: UpdateIssueTemplate :one
UPDATE issue_templates
SET name        = COALESCE(sqlc.narg(name), name),
    title       = COALESCE(sqlc.narg(title), title),
    description = COALESCE(sqlc.narg(description), description),
    priority    = COALESCE(sqlc.narg(priority), priority),
    label_ids   = COALESCE(sqlc.narg(label_ids)::uuid[], label_ids)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteIssueTemplate :exec
DELETE FROM issue_templates WHERE id = $1;

-- name: ListTemplatesForTeams :many
SELECT * FROM issue_templates WHERE team_id = ANY(sqlc.arg(team_ids)::uuid[]) ORDER BY name;

-- name: CreateView :one
INSERT INTO views (id, workspace_id, owner_id, name, shared, team_id, options)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetView :one
SELECT * FROM views WHERE id = $1;

-- name: UpdateView :one
UPDATE views
SET name       = COALESCE(sqlc.narg(name), name),
    shared     = COALESCE(sqlc.narg(shared), shared),
    options    = COALESCE(sqlc.narg(options), options),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteView :exec
DELETE FROM views WHERE id = $1;

-- name: ListViewsForUser :many
-- Shared views, plus the user's own.
SELECT * FROM views
WHERE workspace_id = $1 AND (shared OR owner_id = $2)
ORDER BY lower(name);

-- name: CreateFavorite :one
INSERT INTO favorites (id, workspace_id, user_id, kind, target_id, sort_order)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetFavorite :one
SELECT * FROM favorites WHERE id = $1;

-- name: GetFavoriteByTarget :one
SELECT * FROM favorites WHERE user_id = $1 AND kind = $2 AND target_id = $3;

-- name: DeleteFavorite :exec
DELETE FROM favorites WHERE id = $1;

-- name: ListFavoritesForUser :many
SELECT * FROM favorites WHERE workspace_id = $1 AND user_id = $2 ORDER BY sort_order;

-- name: MaxFavoriteSortOrder :one
SELECT COALESCE(max(sort_order), 0)::double precision FROM favorites WHERE workspace_id = $1 AND user_id = $2;
