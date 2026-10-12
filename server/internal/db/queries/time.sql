-- name: CreateTimeEntry :one
-- Takes a client-chosen ID; a retried create returns no row.
INSERT INTO time_entries (id, workspace_id, issue_id, user_id, started_at, ended_at, seconds, note)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetTimeEntry :one
SELECT * FROM time_entries WHERE id = $1;

-- name: GetRunningTimeEntry :one
SELECT * FROM time_entries WHERE user_id = $1 AND ended_at IS NULL;

-- name: UpdateTimeEntry :one
UPDATE time_entries
SET ended_at = COALESCE(sqlc.narg(ended_at), ended_at),
    seconds  = COALESCE(sqlc.narg(seconds), seconds),
    note     = COALESCE(sqlc.narg(note), note)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteTimeEntry :exec
DELETE FROM time_entries WHERE id = $1;

-- name: ListRecentTimeEntries :many
-- A person's own entries in a workspace: the running timer and the last
-- while, which is what the app shows without asking again.
SELECT * FROM time_entries
WHERE workspace_id = $1 AND user_id = $2 AND (ended_at IS NULL OR started_at > now() - interval '60 days')
ORDER BY started_at;

-- name: SumIssueTime :one
-- Recomputes an issue's logged total from its finished entries.
UPDATE issues
SET logged_seconds = (SELECT COALESCE(sum(t.seconds), 0) FROM time_entries t WHERE t.issue_id = issues.id AND t.ended_at IS NOT NULL),
    updated_at = now()
WHERE issues.id = $1
RETURNING *;

-- name: IssueTimeInProgress :one
-- Seconds the issue has spent in started states, counting the current stay up to now.
SELECT COALESCE(sum(extract(epoch FROM COALESCE(h.left_at, now()) - h.entered_at)), 0)::bigint
FROM issue_state_history h
WHERE h.issue_id = $1 AND h.state_type = 'started';

-- name: ExportTimeEntries :many
-- Finished entries for a timesheet. user_id narrows it to one person.
SELECT t.started_at, t.seconds, t.note, u.email, u.name, tm.key AS team_key, i.number, i.title
FROM time_entries t
JOIN users u ON u.id = t.user_id
JOIN issues i ON i.id = t.issue_id
JOIN teams tm ON tm.id = i.team_id
WHERE t.workspace_id = sqlc.arg(workspace_id)
  AND t.ended_at IS NOT NULL
  AND t.started_at >= sqlc.arg(since)::timestamptz AND t.started_at < sqlc.arg(until)::timestamptz
  AND (sqlc.narg(user_id)::uuid IS NULL OR t.user_id = sqlc.narg(user_id)::uuid)
  AND i.team_id = ANY(sqlc.arg(team_ids)::uuid[])
ORDER BY t.started_at, u.email;
