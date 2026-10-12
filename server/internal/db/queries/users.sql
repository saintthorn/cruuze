-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetOrCreateUserByEmail :one
-- The no-op update makes RETURNING yield the existing row on conflict.
INSERT INTO users (id, email, name)
VALUES ($1, $2, $3)
ON CONFLICT (email) DO UPDATE SET email = users.email
RETURNING *;

-- name: UpdateUserName :one
UPDATE users SET name = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: FillUserProfile :one
-- Fills name and avatar from an identity provider without overwriting user edits.
UPDATE users
SET name       = CASE WHEN name = '' THEN sqlc.arg(name)::text ELSE name END,
    avatar_url = CASE WHEN avatar_url = '' THEN sqlc.arg(avatar_url)::text ELSE avatar_url END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetOAuthAccountUser :one
SELECT u.*
FROM oauth_accounts a
JOIN users u ON u.id = a.user_id
WHERE a.provider = $1 AND a.provider_user_id = $2;

-- name: CreateOAuthAccount :exec
INSERT INTO oauth_accounts (provider, provider_user_id, user_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ListUserMemberships :many
-- Ordered by workspace so callers that lock workspaces do so in a stable order.
SELECT workspace_id, role, created_at AS joined_at
FROM workspace_members
WHERE user_id = $1
ORDER BY workspace_id;
