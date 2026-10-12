-- Reports and forecasts. Weeks start on Monday and are cut in UTC, so every
-- viewer sees the same numbers. Archived issues count: they were still done.

-- name: WeeklyCompletedForTeam :many
SELECT date_trunc('week', i.completed_at AT TIME ZONE 'UTC')::date AS week, count(*)::integer AS issues
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.team_id = $1 AND s.type = 'completed' AND i.completed_at >= sqlc.arg(since)::timestamptz
GROUP BY 1;

-- name: WeeklyCreatedForTeam :many
SELECT date_trunc('week', i.created_at AT TIME ZONE 'UTC')::date AS week, count(*)::integer AS issues
FROM issues i
WHERE i.team_id = $1 AND i.created_at >= sqlc.arg(since)::timestamptz
GROUP BY 1;

-- name: WeeklyCompletedForProject :many
SELECT date_trunc('week', i.completed_at AT TIME ZONE 'UTC')::date AS week, count(*)::integer AS issues
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.project_id = sqlc.arg(project_id) AND i.team_id = ANY(sqlc.arg(team_ids)::uuid[])
  AND s.type = 'completed' AND i.completed_at >= sqlc.arg(since)::timestamptz
GROUP BY 1;

-- name: WeeklyCreatedForProject :many
SELECT date_trunc('week', i.created_at AT TIME ZONE 'UTC')::date AS week, count(*)::integer AS issues
FROM issues i
WHERE i.project_id = sqlc.arg(project_id) AND i.team_id = ANY(sqlc.arg(team_ids)::uuid[])
  AND i.created_at >= sqlc.arg(since)::timestamptz
GROUP BY 1;

-- name: TeamCycleTimes :one
-- How long completed issues took, in days: from first being started to
-- being completed (cycle time) and from being filed to completed (lead time).
SELECT count(*) FILTER (WHERE i.started_at IS NOT NULL)::integer AS cycle_samples,
       (COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM i.completed_at - i.started_at)) FILTER (WHERE i.started_at IS NOT NULL), 0) / 86400.0)::double precision AS cycle_p50,
       (COALESCE(percentile_cont(0.85) WITHIN GROUP (ORDER BY extract(epoch FROM i.completed_at - i.started_at)) FILTER (WHERE i.started_at IS NOT NULL), 0) / 86400.0)::double precision AS cycle_p85,
       (COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM i.completed_at - i.started_at)) FILTER (WHERE i.started_at IS NOT NULL), 0) / 86400.0)::double precision AS cycle_p95,
       count(*)::integer AS lead_samples,
       (COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM i.completed_at - i.created_at)), 0) / 86400.0)::double precision AS lead_p50,
       (COALESCE(percentile_cont(0.85) WITHIN GROUP (ORDER BY extract(epoch FROM i.completed_at - i.created_at)), 0) / 86400.0)::double precision AS lead_p85
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.team_id = $1 AND s.type = 'completed' AND i.completed_at >= sqlc.arg(since)::timestamptz;

-- name: TeamFlow :many
-- How many of a team's issues were in each kind of state at the end of each
-- week: the data behind a cumulative flow diagram.
SELECT (d.day AT TIME ZONE 'UTC')::date AS day, h.state_type, count(*)::integer AS issues
FROM generate_series(sqlc.arg(first)::timestamptz, sqlc.arg(last)::timestamptz, interval '7 days') AS d (day)
JOIN issue_state_history h
  ON h.team_id = sqlc.arg(team_id) AND h.entered_at <= d.day AND (h.left_at IS NULL OR h.left_at > d.day)
GROUP BY 1, 2;

-- name: ProjectScope :one
-- How much of a project (optionally one milestone of it) is left, counting
-- only the teams the caller can see. Archived issues that were completed
-- still count as done.
SELECT count(*) FILTER (WHERE s.type NOT IN ('completed', 'canceled') AND i.archived_at IS NULL)::integer AS remaining,
       count(*) FILTER (WHERE s.type = 'completed')::integer AS completed
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.project_id = sqlc.arg(project_id) AND i.team_id = ANY(sqlc.arg(team_ids)::uuid[])
  AND (sqlc.narg(milestone_id)::uuid IS NULL OR i.milestone_id = sqlc.narg(milestone_id)::uuid);

-- name: CycleScope :one
SELECT count(*) FILTER (WHERE s.type NOT IN ('completed', 'canceled') AND i.archived_at IS NULL)::integer AS remaining,
       count(*) FILTER (WHERE s.type = 'completed')::integer AS completed
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.cycle_id = $1;
