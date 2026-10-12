-- name: CreateWorkflowState :one
INSERT INTO workflow_states (id, workspace_id, team_id, name, type, color, position)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetWorkflowState :one
SELECT * FROM workflow_states WHERE id = $1;

-- name: GetDefaultWorkflowState :one
-- Where new issues land: the first backlog state, else the first state that
-- isn't triage.
SELECT * FROM workflow_states
WHERE team_id = $1 AND type <> 'triage'
ORDER BY (type = 'backlog') DESC, position
LIMIT 1;

-- name: GetTriageState :one
SELECT * FROM workflow_states WHERE team_id = $1 AND type = 'triage' LIMIT 1;

-- name: ListWorkflowStatesForTeams :many
SELECT * FROM workflow_states
WHERE team_id = ANY(sqlc.arg(team_ids)::uuid[])
ORDER BY team_id, position;

-- name: NextIssueNumber :one
-- Allocates the next issue number for a team.
UPDATE teams SET next_issue_number = next_issue_number + 1
WHERE id = $1
RETURNING (next_issue_number - 1)::integer;

-- name: MinIssueSortOrder :one
SELECT COALESCE(min(sort_order), 0)::double precision FROM issues WHERE team_id = $1;

-- name: CreateIssue :one
INSERT INTO issues (id, workspace_id, team_id, number, title, description, state_id, priority, assignee_id, creator_id, sort_order, label_ids, due_date, subscriber_ids, project_id, milestone_id, cycle_id, parent_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
RETURNING *;

-- name: GetIssue :one
SELECT * FROM issues WHERE id = $1;

-- name: GetIssueForUpdate :one
-- Locks the issue so a read-modify-write of its labels can't lose an update.
SELECT * FROM issues WHERE id = $1 FOR UPDATE;

-- name: UpdateIssue :one
-- NULL arguments leave their column unchanged. Columns that can be cleared
-- (assignee, due date) are only touched when their set_ flag is true.
UPDATE issues
SET title       = COALESCE(sqlc.narg(title), title),
    description = COALESCE(sqlc.narg(description), description),
    state_id    = COALESCE(sqlc.narg(state_id), state_id),
    priority    = COALESCE(sqlc.narg(priority), priority),
    sort_order  = COALESCE(sqlc.narg(sort_order), sort_order),
    label_ids   = COALESCE(sqlc.narg(label_ids)::uuid[], label_ids),
    subscriber_ids = COALESCE(sqlc.narg(subscriber_ids)::uuid[], subscriber_ids),
    assignee_id = CASE WHEN sqlc.arg(set_assignee)::boolean THEN sqlc.narg(assignee_id) ELSE assignee_id END,
    due_date    = CASE WHEN sqlc.arg(set_due_date)::boolean THEN sqlc.narg(due_date) ELSE due_date END,
    project_id  = CASE WHEN sqlc.arg(set_project)::boolean THEN sqlc.narg(project_id) ELSE project_id END,
    milestone_id = CASE WHEN sqlc.arg(set_milestone)::boolean THEN sqlc.narg(milestone_id) ELSE milestone_id END,
    cycle_id    = CASE WHEN sqlc.arg(set_cycle)::boolean THEN sqlc.narg(cycle_id) ELSE cycle_id END,
    parent_id   = CASE WHEN sqlc.arg(set_parent)::boolean THEN sqlc.narg(parent_id) ELSE parent_id END,
    updated_at  = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ArchiveIssue :one
UPDATE issues
SET archived_at = COALESCE(archived_at, now()), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListActiveIssuesForTeams :many
SELECT * FROM issues
WHERE team_id = ANY(sqlc.arg(team_ids)::uuid[]) AND archived_at IS NULL;

-- name: SetIssueSubscribers :one
UPDATE issues SET subscriber_ids = $2 WHERE id = $1 RETURNING *;

-- name: GetIssueParent :one
SELECT parent_id FROM issues WHERE id = $1;

-- name: SetIssueCycle :one
UPDATE issues SET cycle_id = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListOpenIssuesInCycles :many
-- Unfinished issues still sitting in the given cycles.
SELECT i.*
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.cycle_id = ANY(sqlc.arg(cycle_ids)::uuid[])
  AND i.archived_at IS NULL
  AND s.type NOT IN ('completed', 'canceled');

-- name: CreateCustomWorkflowState :one
-- Takes a client-chosen ID; a retried create returns no row.
INSERT INTO workflow_states (id, workspace_id, team_id, name, type, color, position)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: UpdateWorkflowState :one
UPDATE workflow_states
SET name       = COALESCE(sqlc.narg(name), name),
    color      = COALESCE(sqlc.narg(color), color),
    position   = COALESCE(sqlc.narg(position), position),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteWorkflowState :exec
DELETE FROM workflow_states WHERE id = $1;

-- name: CountIssuesInState :one
-- Archived issues count too: they still point at the state.
SELECT count(*) FROM issues WHERE state_id = $1;

-- name: ListWorkflowStatesForTeam :many
SELECT * FROM workflow_states WHERE team_id = $1 ORDER BY position;

-- name: UnarchiveIssue :one
UPDATE issues SET archived_at = NULL, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListIssuesToArchive :many
-- Closed issues of a team that have sat past its auto-archive window.
SELECT i.*
FROM issues i
WHERE i.team_id = $1 AND i.archived_at IS NULL AND i.completed_at < sqlc.arg(before)::timestamptz
ORDER BY i.completed_at
LIMIT $2;

-- name: ListTeamsWithAutoArchive :many
SELECT * FROM teams WHERE auto_archive_days > 0;

-- name: TouchIssue :one
-- Marks an issue as looked at: it counts as fresh again.
UPDATE issues SET updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListArchivedIssues :many
-- A team's archived issues, most recently archived first.
SELECT * FROM issues
WHERE team_id = $1 AND archived_at IS NOT NULL AND archived_at < sqlc.arg(before)::timestamptz
ORDER BY archived_at DESC
LIMIT $2;

-- name: ListTeamsWithAutoClose :many
SELECT * FROM teams WHERE auto_close_days > 0;

-- name: ListIssuesToAutoClose :many
-- A team's issues that were never started and that nobody has touched since
-- the cutoff. Work in progress and what waits in triage are left alone.
SELECT i.*
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.team_id = $1 AND i.archived_at IS NULL AND i.completed_at IS NULL
  AND s.type IN ('backlog', 'unstarted')
  AND i.updated_at < sqlc.arg(before)::timestamptz
ORDER BY i.updated_at
LIMIT $2;
