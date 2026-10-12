-- name: CreateLabel :one
INSERT INTO labels (id, workspace_id, name, color)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetLabel :one
SELECT * FROM labels WHERE id = $1;

-- name: ListLabels :many
SELECT * FROM labels WHERE workspace_id = $1 ORDER BY lower(name);

-- name: UpdateLabel :one
UPDATE labels
SET name       = COALESCE(sqlc.narg(name), name),
    color      = COALESCE(sqlc.narg(color), color),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteLabel :exec
DELETE FROM labels WHERE id = $1;

-- name: RemoveLabelFromIssues :exec
UPDATE issues
SET label_ids = array_remove(label_ids, sqlc.arg(label_id)::uuid)
WHERE workspace_id = sqlc.arg(workspace_id) AND sqlc.arg(label_id)::uuid = ANY(label_ids);

-- name: CountLabelsIn :one
-- How many of the given label IDs exist in the workspace.
SELECT count(*) FROM labels
WHERE workspace_id = sqlc.arg(workspace_id) AND id = ANY(sqlc.arg(ids)::uuid[]);
