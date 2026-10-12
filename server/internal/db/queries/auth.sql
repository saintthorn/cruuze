-- name: CreateLoginToken :exec
INSERT INTO login_tokens (id, email, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: CountLoginTokensSince :one
SELECT count(*) FROM login_tokens WHERE email = $1 AND created_at > $2;

-- name: ConsumeLoginToken :one
UPDATE login_tokens
SET consumed_at = now()
WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
RETURNING email;

-- name: DeleteExpiredLoginTokens :exec
DELETE FROM login_tokens WHERE expires_at < now() - interval '1 day';

-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, token_hash, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetSessionUser :one
SELECT sqlc.embed(u), s.id AS session_id, s.last_seen_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE id = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < now();
