-- name: CreateInitiative :one
-- Takes a client-chosen ID; a retried create returns no row.
INSERT INTO initiatives (id, workspace_id, name, description, color, target_date)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetInitiative :one
SELECT * FROM initiatives WHERE id = $1;

-- name: UpdateInitiative :one
UPDATE initiatives
SET name        = COALESCE(sqlc.narg(name), name),
    description = COALESCE(sqlc.narg(description), description),
    color       = COALESCE(sqlc.narg(color), color),
    target_date = CASE WHEN sqlc.arg(set_target_date)::boolean THEN sqlc.narg(target_date) ELSE target_date END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteInitiative :exec
DELETE FROM initiatives WHERE id = $1;

-- name: ListInitiatives :many
SELECT * FROM initiatives WHERE workspace_id = $1 ORDER BY created_at;

-- name: ListProjectsOfInitiative :many
SELECT * FROM projects WHERE initiative_id = $1 ORDER BY created_at;

-- name: CreateProjectDependency :one
INSERT INTO project_dependencies (id, workspace_id, project_id, depends_on_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetProjectDependency :one
SELECT * FROM project_dependencies WHERE id = $1;

-- name: DeleteProjectDependency :exec
DELETE FROM project_dependencies WHERE id = $1;

-- name: ListProjectDependencies :many
SELECT * FROM project_dependencies WHERE workspace_id = $1 ORDER BY created_at;

-- name: CreateProjectUpdate :one
INSERT INTO project_updates (id, workspace_id, project_id, user_id, health, body)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetProjectUpdate :one
SELECT * FROM project_updates WHERE id = $1;

-- name: DeleteProjectUpdate :exec
DELETE FROM project_updates WHERE id = $1;

-- name: ListRecentProjectUpdates :many
-- The latest few updates of every project: enough for its health and a
-- short history without the list growing forever.
SELECT u.id, u.workspace_id, u.project_id, u.user_id, u.health, u.body, u.created_at
FROM (
    SELECT p.*, row_number() OVER (PARTITION BY p.project_id ORDER BY p.created_at DESC) AS position
    FROM project_updates p
    WHERE p.workspace_id = $1
) u
WHERE u.position <= 10
ORDER BY u.created_at;
