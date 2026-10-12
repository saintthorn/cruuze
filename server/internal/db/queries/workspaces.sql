-- name: CreateWorkspace :one
INSERT INTO workspaces (id, name, url_key)
VALUES ($1, $2, $3)
RETURNING *;

-- name: AddWorkspaceMember :exec
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ListWorkspacesForUser :many
SELECT sqlc.embed(w), m.role
FROM workspace_members m
JOIN workspaces w ON w.id = m.workspace_id
WHERE m.user_id = $1
ORDER BY w.created_at;

-- name: GetWorkspaceRole :one
SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2;

-- name: ListWorkspaceMembers :many
SELECT sqlc.embed(u), m.role, m.created_at AS joined_at
FROM workspace_members m
JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = $1
ORDER BY u.name, u.email;

-- name: IsWorkspaceMemberEmail :one
SELECT EXISTS (
    SELECT 1
    FROM workspace_members m
    JOIN users u ON u.id = m.user_id
    WHERE m.workspace_id = $1 AND u.email = $2
);

-- name: CreateWorkspaceInvite :one
INSERT INTO workspace_invites (id, workspace_id, email, role, invited_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListWorkspaceInvites :many
SELECT * FROM workspace_invites WHERE workspace_id = $1 ORDER BY created_at;

-- name: DeleteWorkspaceInvite :execrows
DELETE FROM workspace_invites WHERE id = $1 AND workspace_id = $2;

-- name: AcceptInvitesForEmail :many
-- Turns every pending invite for the address into a membership and returns
-- the memberships that were created.
WITH accepted AS (
    DELETE FROM workspace_invites WHERE email = sqlc.arg(email) RETURNING workspace_id, role
)
INSERT INTO workspace_members (workspace_id, user_id, role)
SELECT workspace_id, sqlc.arg(user_id), role FROM accepted
ON CONFLICT DO NOTHING
RETURNING workspace_id, role, created_at AS joined_at;

-- name: GetWorkspaceMember :one
SELECT sqlc.embed(u), m.role, m.created_at AS joined_at
FROM workspace_members m
JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = $1 AND m.user_id = $2;

-- name: LockWorkspace :exec
-- Serializes changes to a workspace's membership, so that checks like "is
-- there another admin" hold until the transaction commits.
SELECT id FROM workspaces WHERE id = $1 FOR UPDATE;

-- name: UpdateWorkspaceName :one
UPDATE workspaces SET name = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: SetWorkspaceRole :execrows
UPDATE workspace_members SET role = $3 WHERE workspace_id = $1 AND user_id = $2;

-- name: RemoveWorkspaceMember :execrows
DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2;

-- name: CountWorkspaceAdmins :one
SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'admin';

-- name: DeleteMemberAPIKeys :exec
DELETE FROM api_keys WHERE workspace_id = $1 AND user_id = $2;

-- name: DeleteMemberImportConnections :exec
DELETE FROM import_connections WHERE workspace_id = $1 AND user_id = $2;

-- name: DeleteMemberNotifications :exec
DELETE FROM notifications WHERE workspace_id = $1 AND user_id = $2;

-- name: DeleteMemberFavorites :exec
DELETE FROM favorites WHERE workspace_id = $1 AND user_id = $2;

-- name: DeleteMemberPrivateViews :exec
DELETE FROM views WHERE workspace_id = $1 AND owner_id = $2 AND NOT shared;
