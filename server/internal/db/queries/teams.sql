-- name: CreateTeam :one
INSERT INTO teams (id, workspace_id, name, key, private, kind, triage_enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetTeam :one
SELECT * FROM teams WHERE id = $1;

-- name: ListTeamsForUser :many
-- Teams the user belongs to, plus (unless members_only) every public team.
SELECT sqlc.embed(t), (tm.user_id IS NOT NULL)::boolean AS is_member
FROM teams t
LEFT JOIN team_members tm ON tm.team_id = t.id AND tm.user_id = sqlc.arg(user_id)
WHERE t.workspace_id = sqlc.arg(workspace_id)
  AND (tm.user_id IS NOT NULL OR (NOT t.private AND NOT sqlc.arg(members_only)::boolean))
ORDER BY t.name;

-- name: AddTeamMember :execrows
INSERT INTO team_members (team_id, user_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveTeamMember :execrows
DELETE FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: IsTeamMember :one
SELECT EXISTS (SELECT 1 FROM team_members WHERE team_id = $1 AND user_id = $2);

-- name: ListTeamMembers :many
SELECT u.*
FROM team_members tm
JOIN users u ON u.id = tm.user_id
WHERE tm.team_id = $1
ORDER BY u.name, u.email;

-- name: ListMemberTeamIDs :many
-- The teams within a workspace that a user belongs to.
SELECT tm.team_id
FROM team_members tm
JOIN teams t ON t.id = tm.team_id
WHERE t.workspace_id = $1 AND tm.user_id = $2;

-- name: ListTeamMembersForTeams :many
SELECT team_id, user_id
FROM team_members
WHERE team_id = ANY(sqlc.arg(team_ids)::uuid[]);

-- name: ListUsersWhoCanSeeTeam :many
-- Of the given users, those who may see the team: its members, plus every
-- non-guest workspace member if the team is public.
SELECT m.user_id
FROM workspace_members m
JOIN teams t ON t.id = sqlc.arg(team_id) AND t.workspace_id = m.workspace_id
WHERE m.user_id = ANY(sqlc.arg(user_ids)::uuid[])
  AND (
      EXISTS (SELECT 1 FROM team_members tm WHERE tm.team_id = t.id AND tm.user_id = m.user_id)
      OR (NOT t.private AND m.role <> 'guest')
  );

-- name: UpdateTeamSettings :one
UPDATE teams
SET name           = COALESCE(sqlc.narg(name), name),
    cycle_weeks    = COALESCE(sqlc.narg(cycle_weeks), cycle_weeks),
    triage_enabled = COALESCE(sqlc.narg(triage_enabled), triage_enabled),
    private        = COALESCE(sqlc.narg(private), private),
    kind           = COALESCE(sqlc.narg(kind), kind),
    auto_archive_days = COALESCE(sqlc.narg(auto_archive_days), auto_archive_days),
    stale_days        = COALESCE(sqlc.narg(stale_days), stale_days),
    auto_close_days   = COALESCE(sqlc.narg(auto_close_days), auto_close_days),
    auto_close_parent = COALESCE(sqlc.narg(auto_close_parent), auto_close_parent),
    auto_assign       = COALESCE(sqlc.narg(auto_assign), auto_assign),
    auto_cycle        = COALESCE(sqlc.narg(auto_cycle), auto_cycle),
    review_state_id   = CASE WHEN sqlc.arg(set_review_state)::boolean THEN sqlc.narg(review_state_id) ELSE review_state_id END,
    updated_at     = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListTeamsWithCycles :many
SELECT * FROM teams WHERE cycle_weeks > 0;

-- name: LockTeamCycles :exec
-- Serializes cycle upkeep for a team across server instances.
SELECT pg_advisory_xact_lock(hashtext('cycles:' || sqlc.arg(team_id)::text));

-- name: ListMemberTeams :many
-- The teams within a workspace that a user belongs to.
SELECT t.*
FROM team_members tm
JOIN teams t ON t.id = tm.team_id
WHERE t.workspace_id = $1 AND tm.user_id = $2
ORDER BY t.id;

-- name: DeleteTeam :exec
-- Everything the team owns (issues, statuses, cycles, members) goes with it.
DELETE FROM teams WHERE id = $1;
