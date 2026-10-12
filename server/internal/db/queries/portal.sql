-- name: GetWorkspaceByURLKey :one
SELECT * FROM workspaces WHERE url_key = $1;

-- name: UpdateWorkspacePortal :one
UPDATE workspaces
SET portal_enabled      = COALESCE(sqlc.narg(enabled), portal_enabled),
    portal_public_board = COALESCE(sqlc.narg(public_board), portal_public_board),
    portal_intro        = COALESCE(sqlc.narg(intro), portal_intro),
    updated_at          = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CreateCustomerRequest :one
-- Takes a client-chosen ID; a retried create returns no row.
INSERT INTO customer_requests (id, workspace_id, requester_email, requester_id, title, body, issue_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetCustomerRequest :one
SELECT * FROM customer_requests WHERE id = $1;

-- name: GetCustomerRequestForUpdate :one
SELECT * FROM customer_requests WHERE id = $1 FOR UPDATE;

-- name: UpdateCustomerRequest :one
-- Entering 'shipped' stamps shipped_at; the requester is told a little later.
UPDATE customer_requests
SET title      = COALESCE(sqlc.narg(title), title),
    body       = COALESCE(sqlc.narg(body), body),
    status     = COALESCE(sqlc.narg(status), status),
    public     = COALESCE(sqlc.narg(public), public),
    response   = COALESCE(sqlc.narg(response), response),
    issue_id   = CASE WHEN sqlc.arg(set_issue)::boolean THEN sqlc.narg(issue_id) ELSE issue_id END,
    shipped_at = CASE WHEN sqlc.narg(status)::text = 'shipped' AND status <> 'shipped' THEN now() ELSE shipped_at END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteCustomerRequest :exec
DELETE FROM customer_requests WHERE id = $1;

-- name: ListCustomerRequests :many
-- What the team works from: everything still open, and what was settled lately.
SELECT * FROM customer_requests
WHERE workspace_id = $1 AND (status NOT IN ('shipped', 'declined') OR updated_at > now() - interval '90 days')
ORDER BY created_at;

-- name: CountRecentCustomerRequests :one
-- How many requests an address has sent to a workspace since the given time.
SELECT count(*) FROM customer_requests
WHERE workspace_id = $1 AND requester_email = $2 AND requester_id IS NOT NULL AND created_at > $3;

-- name: ShipRequestsForIssue :many
-- The issue was completed: what was waiting on it has shipped.
UPDATE customer_requests
SET status = 'shipped', shipped_at = now(), updated_at = now()
WHERE issue_id = $1 AND status IN ('new', 'reviewing', 'planned')
RETURNING *;

-- name: UnshipRequestsForIssue :many
-- The issue was reopened before anyone was told: back to planned.
UPDATE customer_requests
SET status = 'planned', shipped_at = NULL, updated_at = now()
WHERE issue_id = $1 AND status = 'shipped' AND notified_at IS NULL
RETURNING *;

-- name: ListPortalRequests :many
-- A portal visitor's view: their own requests, and the public board if on.
SELECT r.*,
       COALESCE(r.requester_id = sqlc.narg(user_id)::uuid OR r.requester_email = sqlc.narg(email)::citext, false)::boolean AS mine,
       EXISTS (SELECT 1 FROM customer_request_votes v WHERE v.request_id = r.id AND v.user_id = sqlc.narg(user_id)::uuid)::boolean AS voted
FROM customer_requests r
WHERE r.workspace_id = sqlc.arg(workspace_id)
  AND (COALESCE(r.requester_id = sqlc.narg(user_id)::uuid, false)
       OR COALESCE(r.requester_email = sqlc.narg(email)::citext, false)
       OR (sqlc.arg(board)::boolean AND r.public AND r.status <> 'declined'))
ORDER BY r.vote_count DESC, r.created_at DESC
LIMIT 500;

-- name: AddRequestVote :execrows
INSERT INTO customer_request_votes (request_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: RemoveRequestVote :execrows
DELETE FROM customer_request_votes WHERE request_id = $1 AND user_id = $2;

-- name: RecountRequestVotes :one
UPDATE customer_requests
SET vote_count = (SELECT count(*) FROM customer_request_votes v WHERE v.request_id = customer_requests.id)
WHERE customer_requests.id = $1
RETURNING *;

-- name: ClaimShippedRequests :many
-- Takes the shipped requests whose requesters haven't been told, marking
-- them so no other server tells them too.
WITH claimed AS (
    UPDATE customer_requests
    SET notified_at = now()
    WHERE id IN (
        SELECT p.id FROM customer_requests p
        WHERE p.status = 'shipped' AND p.notified_at IS NULL AND p.shipped_at <= sqlc.arg(before)::timestamptz
        ORDER BY p.shipped_at
        LIMIT 200
        FOR UPDATE SKIP LOCKED
    )
    RETURNING id, workspace_id, requester_email, title, response
)
SELECT c.id, c.requester_email, c.title, c.response, w.name AS workspace_name, w.url_key, w.portal_enabled
FROM claimed c
JOIN workspaces w ON w.id = c.workspace_id;

-- name: ListRequestVoterEmails :many
SELECT u.email FROM customer_request_votes v JOIN users u ON u.id = v.user_id WHERE v.request_id = $1;
