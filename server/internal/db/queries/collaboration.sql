-- name: CreateComment :one
INSERT INTO comments (id, workspace_id, issue_id, user_id, body)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetComment :one
SELECT * FROM comments WHERE id = $1;

-- name: UpdateCommentBody :one
UPDATE comments SET body = $2, edited_at = now() WHERE id = $1 RETURNING *;

-- name: DeleteComment :exec
DELETE FROM comments WHERE id = $1;

-- name: AddReaction :execrows
INSERT INTO comment_reactions (comment_id, user_id, emoji)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: RemoveReaction :execrows
DELETE FROM comment_reactions WHERE comment_id = $1 AND user_id = $2 AND emoji = $3;

-- name: CreateIssueActivity :one
INSERT INTO issue_activities (id, workspace_id, issue_id, actor_id, type, from_value, to_value, added_label_ids, removed_label_ids)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: CreateNotification :one
INSERT INTO notifications (id, workspace_id, user_id, issue_id, actor_id, comment_id, type)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetNotification :one
SELECT * FROM notifications WHERE id = $1;

-- name: SetNotificationRead :one
UPDATE notifications
SET read_at = CASE WHEN sqlc.arg(read)::boolean THEN COALESCE(read_at, now()) ELSE NULL END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteNotification :exec
DELETE FROM notifications WHERE id = $1;

-- name: ListNotificationsForUser :many
-- The newest notifications; older ones stay on the server.
SELECT * FROM notifications
WHERE workspace_id = $1 AND user_id = $2
ORDER BY created_at DESC
LIMIT 500;

-- name: CreateAttachment :one
INSERT INTO attachments (id, workspace_id, issue_id, uploader_id, filename, content_type, size, storage_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetAttachment :one
SELECT * FROM attachments WHERE id = $1;

-- name: DeleteAttachment :exec
DELETE FROM attachments WHERE id = $1;

-- name: ListAttachmentsForTeams :many
SELECT a.*
FROM attachments a
JOIN issues i ON i.id = a.issue_id
WHERE i.team_id = ANY(sqlc.arg(team_ids)::uuid[]) AND i.archived_at IS NULL
ORDER BY a.created_at;

-- name: ListCommentsForIssue :many
SELECT * FROM comments WHERE issue_id = $1 ORDER BY created_at;

-- name: ListReactionsForIssue :many
SELECT r.comment_id, r.user_id, r.emoji
FROM comment_reactions r
JOIN comments c ON c.id = r.comment_id
WHERE c.issue_id = $1
ORDER BY r.created_at;

-- name: ListActivitiesForIssue :many
SELECT * FROM issue_activities WHERE issue_id = $1 ORDER BY created_at;

-- name: ListCommentsForNotifications :many
-- The comments a user's newest notifications are about, which the inbox
-- quotes. Kept in step with ListNotificationsForUser.
-- Limited to the given teams: a notification can outlive the recipient's
-- access to the issue it is about.
SELECT c.*
FROM comments c
JOIN issues i ON i.id = c.issue_id
WHERE c.id IN (
    SELECT n.comment_id FROM notifications n
    WHERE n.workspace_id = sqlc.arg(workspace_id) AND n.user_id = sqlc.arg(user_id) AND n.comment_id IS NOT NULL
    ORDER BY n.created_at DESC
    LIMIT 500
)
  AND i.team_id = ANY(sqlc.arg(team_ids)::uuid[]) AND i.archived_at IS NULL
ORDER BY c.created_at;
