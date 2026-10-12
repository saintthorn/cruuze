-- name: NextSyncID :one
-- Allocates the next sync ID. The row lock is held until the transaction
-- commits, which is what keeps a workspace's sync IDs in commit order.
UPDATE workspaces SET last_sync_id = last_sync_id + 1 WHERE id = $1 RETURNING last_sync_id;

-- name: GetLastSyncID :one
SELECT last_sync_id FROM workspaces WHERE id = $1;

-- name: InsertSyncAction :exec
INSERT INTO sync_actions (workspace_id, sync_id, model, model_id, action, data, team_id, team_private, user_id, resets, members_only)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: NotifySync :exec
-- Delivered to listeners when the surrounding transaction commits.
SELECT pg_notify('cruuze_sync', sqlc.arg(workspace_id)::text);

-- name: ListSyncActionsAfter :many
SELECT sync_id, model, model_id, action, data, team_id, team_private, user_id, resets, members_only
FROM sync_actions
WHERE workspace_id = $1 AND sync_id > $2
ORDER BY sync_id
LIMIT $3;

-- name: HasSyncResetAfter :one
-- Whether anything after the given sync ID changed who may see what.
SELECT EXISTS (
    SELECT 1 FROM sync_actions WHERE workspace_id = $1 AND sync_id > $2 AND resets
)::boolean;

-- name: GetOldestSyncIDAfter :one
-- 0 when there is nothing after the given sync ID.
SELECT COALESCE(min(sync_id), 0)::bigint
FROM sync_actions
WHERE workspace_id = $1 AND sync_id > $2;

-- name: DeleteOldSyncActions :exec
-- Clients that fall further behind than this bootstrap again.
DELETE FROM sync_actions WHERE created_at < now() - interval '30 days';
