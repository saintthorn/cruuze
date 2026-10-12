-- name: SearchIssues :many
-- Full-text search over the issues of the given teams and their comments,
-- archived ones included. query is a tsquery; an issue is returned once,
-- with its best match.
WITH q AS (SELECT to_tsquery('english', sqlc.arg(query)::text) AS query),
hits AS (
    SELECT i.id AS issue_id,
           ts_rank(setweight(to_tsvector('english', i.title), 'A') || setweight(to_tsvector('english', i.description), 'B'), q.query) AS rank,
           ts_headline('english', i.description, q.query, 'MaxFragments=1,MaxWords=24,MinWords=8,StartSel=<mark>,StopSel=</mark>') AS snippet,
           false AS in_comment
    FROM issues i, q
    WHERE i.team_id = ANY(sqlc.arg(team_ids)::uuid[])
      AND (setweight(to_tsvector('english', i.title), 'A') || setweight(to_tsvector('english', i.description), 'B')) @@ q.query
    UNION ALL
    SELECT c.issue_id,
           ts_rank(to_tsvector('english', c.body), q.query) * 0.4 AS rank,
           ts_headline('english', c.body, q.query, 'MaxFragments=1,MaxWords=24,MinWords=8,StartSel=<mark>,StopSel=</mark>') AS snippet,
           true AS in_comment
    FROM comments c
    JOIN issues i ON i.id = c.issue_id
    CROSS JOIN q
    WHERE i.team_id = ANY(sqlc.arg(team_ids)::uuid[])
      AND to_tsvector('english', c.body) @@ q.query
)
SELECT best.issue_id::uuid AS issue_id, best.rank::real AS rank, best.snippet::text AS snippet, best.in_comment::boolean AS in_comment,
       t.key AS team_key, i.number, i.title, i.state_id, (i.archived_at IS NOT NULL)::boolean AS archived
FROM (
    SELECT DISTINCT ON (hits.issue_id) hits.issue_id, hits.rank, hits.snippet, hits.in_comment
    FROM hits
    ORDER BY hits.issue_id, hits.rank DESC
) best
JOIN issues i ON i.id = best.issue_id
JOIN teams t ON t.id = i.team_id
-- What is still live comes before what has been put away.
ORDER BY (i.archived_at IS NOT NULL), best.rank DESC
LIMIT 50;

-- name: CreateAPIKey :one
INSERT INTO api_keys (id, workspace_id, user_id, name, token_hash, hint)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAPIKeys :many
SELECT * FROM api_keys WHERE workspace_id = $1 AND user_id = $2 ORDER BY created_at;

-- name: DeleteAPIKey :execrows
DELETE FROM api_keys WHERE id = $1 AND user_id = $2;

-- name: GetAPIKeyUser :one
-- The key's owner, as long as they still belong to the key's workspace.
SELECT sqlc.embed(u), k.id AS key_id, k.workspace_id, k.last_used_at
FROM api_keys k
JOIN users u ON u.id = k.user_id
JOIN workspace_members m ON m.workspace_id = k.workspace_id AND m.user_id = k.user_id
WHERE k.token_hash = $1;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;

-- name: CreateWebhook :one
INSERT INTO webhooks (id, workspace_id, label, url, secret, team_id, models, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetWebhook :one
SELECT * FROM webhooks WHERE id = $1;

-- name: ListWebhooks :many
SELECT * FROM webhooks WHERE workspace_id = $1 ORDER BY created_at;

-- name: UpdateWebhook :one
UPDATE webhooks
SET label   = COALESCE(sqlc.narg(label), label),
    url     = COALESCE(sqlc.narg(url), url),
    models  = COALESCE(sqlc.narg(models)::text[], models),
    enabled = COALESCE(sqlc.narg(enabled), enabled),
    -- Whoever changed it last answers for where it sends.
    created_by = sqlc.arg(updated_by)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteWebhook :exec
DELETE FROM webhooks WHERE id = $1;

-- name: EnqueueWebhookDeliveries :exec
-- Queues one delivery per webhook that wants this change. A webhook without a
-- team gets everything that isn't in a private team; one with a team gets
-- only that team's changes, and only while the person who set it up may see
-- the team themselves.
INSERT INTO webhook_deliveries (webhook_id, model, action, payload)
SELECT w.id, sqlc.arg(model)::text, sqlc.arg(action)::text, sqlc.arg(payload)::jsonb
FROM webhooks w
WHERE w.workspace_id = sqlc.arg(workspace_id)
  AND w.enabled
  AND sqlc.arg(model)::text = ANY(w.models)
  AND CASE WHEN w.team_id IS NULL THEN NOT sqlc.arg(team_private)::boolean
           ELSE w.team_id = sqlc.narg(team_id)::uuid AND (
               EXISTS (SELECT 1 FROM team_members tm WHERE tm.team_id = w.team_id AND tm.user_id = w.created_by)
               OR (NOT sqlc.arg(team_private)::boolean AND EXISTS (
                   SELECT 1 FROM workspace_members m
                   WHERE m.workspace_id = w.workspace_id AND m.user_id = w.created_by AND m.role <> 'guest'))
           ) END;

-- name: ClaimWebhookDeliveries :many
-- Takes due deliveries for sending. Pushing next_attempt_at out is a lease:
-- if the sender dies, the delivery comes due again by itself.
UPDATE webhook_deliveries d
SET attempts = d.attempts + 1, next_attempt_at = now() + interval '10 minutes'
FROM webhooks w
WHERE w.id = d.webhook_id
  AND d.id IN (
      SELECT p.id FROM webhook_deliveries p
      WHERE p.status = 'pending' AND p.next_attempt_at <= now()
      ORDER BY p.next_attempt_at
      LIMIT $1
      FOR UPDATE SKIP LOCKED
  )
RETURNING d.id, d.model, d.payload, d.attempts, w.url, w.secret, w.enabled;

-- name: FinishWebhookDelivery :exec
UPDATE webhook_deliveries
SET status          = sqlc.arg(status),
    response_status = sqlc.narg(response_status),
    error           = sqlc.arg(error),
    next_attempt_at = sqlc.arg(next_attempt_at),
    finished_at     = CASE WHEN sqlc.arg(status)::text = 'pending' THEN NULL ELSE now() END
WHERE id = sqlc.arg(id);

-- name: ListWebhookDeliveries :many
SELECT id, model, action, status, attempts, response_status, error, created_at, finished_at
FROM webhook_deliveries
WHERE webhook_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 50;

-- name: DeleteOldWebhookDeliveries :exec
DELETE FROM webhook_deliveries WHERE created_at < now() - interval '7 days';

-- name: GetGithubIntegration :one
SELECT * FROM github_integrations WHERE workspace_id = $1;

-- name: UpsertGithubIntegration :one
INSERT INTO github_integrations (workspace_id, secret, created_by)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id) DO UPDATE SET secret = EXCLUDED.secret
RETURNING *;

-- name: DeleteGithubIntegration :exec
DELETE FROM github_integrations WHERE workspace_id = $1;

-- name: GetIssueByTeamKeyAndNumber :one
SELECT i.*
FROM issues i
JOIN teams t ON t.id = i.team_id
WHERE t.workspace_id = $1 AND t.key = $2 AND i.number = $3;

-- name: ListPullRequestsByURL :many
SELECT * FROM pull_requests WHERE workspace_id = $1 AND url = $2;

-- name: CreatePullRequest :one
INSERT INTO pull_requests (id, workspace_id, issue_id, url, repo, number, title, branch, author, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdatePullRequest :one
UPDATE pull_requests
SET title = $2, branch = $3, status = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CountOtherOpenPullRequests :one
-- Pull requests still in flight for the issue, other than the given one.
SELECT count(*) FROM pull_requests
WHERE issue_id = $1 AND id <> $2 AND status IN ('draft', 'open');

-- name: ListPullRequestsForTeams :many
SELECT p.*
FROM pull_requests p
JOIN issues i ON i.id = p.issue_id
WHERE i.team_id = ANY(sqlc.arg(team_ids)::uuid[]) AND i.archived_at IS NULL
ORDER BY p.created_at;

-- name: GetFirstWorkflowStateOfType :one
SELECT * FROM workflow_states WHERE team_id = $1 AND type = $2 ORDER BY position LIMIT 1;

-- name: ReserveIssueNumbers :one
-- Allocates a run of issue numbers for a team and returns the first.
UPDATE teams SET next_issue_number = next_issue_number + sqlc.arg(count)::integer
WHERE id = sqlc.arg(id)
RETURNING (next_issue_number - sqlc.arg(count)::integer)::integer;

-- name: UpdateUserEmailNotifications :one
UPDATE users SET email_notifications = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ClaimNotificationsToEmail :many
-- Takes the unread notifications created before the cutoff that haven't been
-- emailed, marking them so no other server sends them too. Those about an
-- issue the person can no longer see are marked and left out.
WITH claimed AS (
    UPDATE notifications
    SET emailed_at = now()
    WHERE id IN (
        SELECT p.id FROM notifications p
        WHERE p.emailed_at IS NULL AND p.read_at IS NULL AND p.created_at <= sqlc.arg(before)::timestamptz
        ORDER BY p.created_at
        LIMIT 500
        FOR UPDATE SKIP LOCKED
    )
    RETURNING id, workspace_id, user_id, issue_id, actor_id, comment_id, type, created_at
)
SELECT n.id, n.type, n.user_id, n.workspace_id,
       u.email, u.email_notifications,
       w.name AS workspace_name, w.url_key,
       t.key AS team_key, i.number, i.title,
       COALESCE(NULLIF(a.name, ''), a.email, '')::text AS actor_name,
       COALESCE(c.body, '')::text AS comment_body
FROM claimed n
JOIN users u ON u.id = n.user_id
JOIN workspaces w ON w.id = n.workspace_id
JOIN issues i ON i.id = n.issue_id
JOIN teams t ON t.id = i.team_id
JOIN workspace_members m ON m.workspace_id = n.workspace_id AND m.user_id = n.user_id
LEFT JOIN users a ON a.id = n.actor_id
LEFT JOIN comments c ON c.id = n.comment_id
WHERE EXISTS (SELECT 1 FROM team_members tm WHERE tm.team_id = t.id AND tm.user_id = n.user_id)
   OR (NOT t.private AND m.role <> 'guest')
ORDER BY n.user_id, n.workspace_id, n.created_at;

-- name: GetCurrentCycle :one
-- The team's cycle that contains the given day.
SELECT * FROM cycles WHERE team_id = $1 AND starts_at <= sqlc.arg(day)::date AND ends_at >= sqlc.arg(day)::date LIMIT 1;

-- name: ListSubIssueStateTypes :many
-- The kind of state each of an issue's (unarchived) sub-issues is in.
SELECT s.type
FROM issues i
JOIN workflow_states s ON s.id = i.state_id
WHERE i.parent_id = $1 AND i.archived_at IS NULL;

-- name: GetMemberByGithubLogin :one
-- The workspace member who gave this GitHub username, if exactly one did.
SELECT u.*
FROM users u
JOIN workspace_members m ON m.user_id = u.id
WHERE m.workspace_id = $1 AND lower(u.github_login) = lower(sqlc.arg(login)::text) AND u.github_login <> ''
ORDER BY m.created_at
LIMIT 1;

-- name: UpdateUserGithubLogin :one
UPDATE users SET github_login = $2, updated_at = now() WHERE id = $1 RETURNING *;
