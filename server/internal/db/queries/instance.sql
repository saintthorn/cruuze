-- name: GetInstanceSettings :one
SELECT * FROM instance_settings;

-- name: UpdateInstanceSettings :one
UPDATE instance_settings
SET signup             = COALESCE(sqlc.narg(signup), signup),
    signup_domains     = COALESCE(sqlc.narg(signup_domains)::text[], signup_domains),
    workspace_creation = COALESCE(sqlc.narg(workspace_creation), workspace_creation),
    updated_at         = now()
RETURNING *;

-- name: ClaimInstanceAdmin :execrows
-- The only user of a new install becomes its admin.
UPDATE users SET instance_admin = true
WHERE users.id = $1 AND NOT users.instance_admin AND (SELECT count(*) FROM users) = 1;

-- name: SignupStanding :one
-- What is known about an address that wants to sign in.
SELECT EXISTS (SELECT 1 FROM users u WHERE u.email = sqlc.arg(email)::citext)::boolean AS known,
       EXISTS (SELECT 1 FROM workspace_invites i WHERE i.email = sqlc.arg(email)::citext)::boolean AS invited,
       (NOT EXISTS (SELECT 1 FROM users))::boolean AS first;

-- name: IsMemberAnywhere :one
SELECT EXISTS (SELECT 1 FROM workspace_members m WHERE m.user_id = $1)::boolean;

-- name: InstanceStats :one
SELECT (SELECT count(*) FROM users)::bigint AS users,
       (SELECT count(*) FROM workspaces)::bigint AS workspaces,
       (SELECT count(*) FROM issues)::bigint AS issues;

-- name: ListInstanceAdminEmails :many
SELECT email::text FROM users WHERE instance_admin ORDER BY created_at;
