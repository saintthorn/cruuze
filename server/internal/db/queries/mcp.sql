-- Queries behind the assistant tools (internal/server/mcp_tools.go): reads
-- that the web app does from its local copy have to be asked of the database.

-- name: GetWorkspace :one
SELECT * FROM workspaces WHERE id = $1;

-- name: ListIssuesFiltered :many
-- Issues of the given teams that pass every filter that is set. Array
-- filters match any of their values; an empty array is no filter.
SELECT i.*
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.team_id = ANY(sqlc.arg(team_ids)::uuid[])
  AND (sqlc.arg(include_archived)::boolean OR i.archived_at IS NULL)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR i.assignee_id = sqlc.narg(assignee_id)::uuid)
  AND (NOT sqlc.arg(unassigned)::boolean OR i.assignee_id IS NULL)
  AND (cardinality(sqlc.arg(state_types)::text[]) = 0 OR s.type = ANY(sqlc.arg(state_types)::text[]))
  AND (cardinality(sqlc.arg(state_ids)::uuid[]) = 0 OR i.state_id = ANY(sqlc.arg(state_ids)::uuid[]))
  AND (cardinality(sqlc.arg(cycle_ids)::uuid[]) = 0 OR i.cycle_id = ANY(sqlc.arg(cycle_ids)::uuid[]))
  AND (sqlc.narg(project_id)::uuid IS NULL OR i.project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(label_id)::uuid IS NULL OR sqlc.narg(label_id)::uuid = ANY(i.label_ids))
  AND (sqlc.narg(parent_id)::uuid IS NULL OR i.parent_id = sqlc.narg(parent_id)::uuid)
  AND (sqlc.narg(due_by)::date IS NULL OR i.due_date <= sqlc.narg(due_by)::date)
  AND (sqlc.narg(untouched_since)::timestamptz IS NULL OR i.updated_at < sqlc.narg(untouched_since)::timestamptz)
ORDER BY
  CASE WHEN sqlc.arg(sort)::text = 'priority' THEN CASE WHEN i.priority = 0 THEN 5 ELSE i.priority END END,
  CASE WHEN sqlc.arg(sort)::text = 'due' THEN i.due_date END NULLS LAST,
  CASE WHEN sqlc.arg(sort)::text = 'created' THEN i.created_at END DESC,
  i.updated_at DESC
LIMIT sqlc.arg(max_rows);

-- name: ListSubIssues :many
SELECT * FROM issues WHERE parent_id = $1 AND archived_at IS NULL ORDER BY sort_order;

-- name: GetIssuesByIDs :many
SELECT * FROM issues WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListRelationsForIssue :many
SELECT * FROM issue_relations WHERE issue_id = $1 OR related_issue_id = $1 ORDER BY created_at;

-- name: ListPullRequestsForIssue :many
SELECT * FROM pull_requests WHERE issue_id = $1 ORDER BY created_at;

-- name: ListCustomerRequestsForIssue :many
SELECT * FROM customer_requests WHERE issue_id = $1 ORDER BY created_at;

-- name: ListUpdatesOfProject :many
SELECT * FROM project_updates WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: ListDependenciesOfProject :many
SELECT * FROM project_dependencies WHERE project_id = $1 OR depends_on_id = $1;

-- name: ListCurrentCycles :many
-- The cycle of each given team that contains the given day.
SELECT * FROM cycles
WHERE team_id = ANY(sqlc.arg(team_ids)::uuid[]) AND starts_at <= sqlc.arg(day)::date AND ends_at >= sqlc.arg(day)::date;
