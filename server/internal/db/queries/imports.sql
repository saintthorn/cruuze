-- name: CreateImport :one
INSERT INTO imports (id, workspace_id, source, label, stage, created_by, request, origin, secret)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetLastCompleteImport :one
-- The newest import of an origin that ran to the end: when it started and
-- what it was asked for, so the next one need only read what changed since.
SELECT created_at, request FROM imports
WHERE workspace_id = $1 AND origin = $2 AND origin <> '' AND status = 'done'
ORDER BY created_at DESC
LIMIT 1;

-- name: GetImport :one
SELECT * FROM imports WHERE id = $1;

-- name: ListImports :many
SELECT * FROM imports WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 50;

-- name: UpdateImportProgress :exec
UPDATE imports
SET stage = $2, counts = $3, heartbeat_at = now()
WHERE id = $1 AND status = 'running';

-- name: FinishImport :one
UPDATE imports
SET status = $2, error = $3, counts = $4, warnings = $5, stage = '', finished_at = now(), secret = NULL
WHERE id = $1
RETURNING *;

-- name: MarkImportUndone :one
UPDATE imports SET status = 'undone', stage = '', finished_at = now(), secret = NULL WHERE id = $1 RETURNING *;

-- name: FailStaleImports :exec
-- Imports whose server went away mid-run, and that can't be carried on
-- with: nothing was kept to do so, or it has been tried enough.
UPDATE imports
SET status = 'failed', stage = '', finished_at = now(), secret = NULL,
    error = 'The server stopped while this import was running. Run it again: what was already imported is not duplicated.'
WHERE workspace_id = $1 AND status = 'running' AND heartbeat_at < now() - interval '3 minutes'
  AND (secret IS NULL OR resumes >= 3);

-- name: ClaimImportsToResume :many
-- Running imports nobody has heard from, which kept what it takes to carry
-- on. Claiming one makes it this server's: no other will pick it up.
UPDATE imports
SET heartbeat_at = now(), resumes = resumes + 1, stage = 'Resuming'
WHERE id IN (
    SELECT i.id FROM imports i
    WHERE i.status = 'running' AND i.secret IS NOT NULL AND i.resumes < 3
      AND i.heartbeat_at < now() - interval '90 seconds'
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ReleaseImport :exec
-- A server that is shutting down hands its import to whichever starts next.
UPDATE imports SET heartbeat_at = 'epoch', stage = 'Resuming' WHERE id = $1 AND status = 'running';

-- name: UpsertImportConnection :one
INSERT INTO import_connections (id, workspace_id, user_id, provider, label, secret)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (workspace_id, user_id, provider) DO UPDATE
SET label = EXCLUDED.label, secret = EXCLUDED.secret, updated_at = now()
RETURNING *;

-- name: GetImportConnection :one
SELECT * FROM import_connections WHERE workspace_id = $1 AND user_id = $2 AND provider = $3;

-- name: ListImportConnections :many
SELECT * FROM import_connections WHERE workspace_id = $1 AND user_id = $2 ORDER BY provider;

-- name: SetImportConnectionSecret :exec
UPDATE import_connections SET secret = $2, updated_at = now() WHERE id = $1;

-- name: DeleteImportConnection :execrows
DELETE FROM import_connections WHERE workspace_id = $1 AND user_id = $2 AND provider = $3;

-- name: CountRunningImports :one
SELECT count(*) FROM imports WHERE workspace_id = $1 AND status = 'running';

-- name: UpsertImportItems :batchexec
INSERT INTO import_items (workspace_id, origin, kind, external_id, record_id, created_by, source_updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (workspace_id, origin, kind, external_id) DO UPDATE
SET record_id = EXCLUDED.record_id,
    -- Who created the record doesn't change when a later import updates it,
    -- unless the record had to be made anew.
    created_by = CASE WHEN import_items.record_id = EXCLUDED.record_id THEN import_items.created_by ELSE EXCLUDED.created_by END,
    source_updated_at = EXCLUDED.source_updated_at;

-- name: ListImportItems :many
-- Everything of one kind an origin has brought in so far.
SELECT external_id, record_id, source_updated_at FROM import_items
WHERE workspace_id = $1 AND origin = $2 AND kind = $3;

-- name: ListImportedIssues :many
-- The issues among the given external IDs that were imported before and
-- still exist, outside the teams the person importing can't see.
SELECT m.external_id, m.record_id, m.source_updated_at, i.team_id, i.number
FROM import_items m
JOIN issues i ON i.id = m.record_id
WHERE m.workspace_id = $1 AND m.origin = $2 AND m.kind = 'issue' AND m.external_id = ANY(sqlc.arg(external_ids)::text[])
  AND NOT (i.team_id = ANY(sqlc.arg(hidden_team_ids)::uuid[]));

-- name: ListImportedComments :many
SELECT m.external_id, m.record_id
FROM import_items m
JOIN comments c ON c.id = m.record_id
JOIN issues i ON i.id = c.issue_id
WHERE m.workspace_id = $1 AND m.origin = $2 AND m.kind = 'comment' AND m.external_id = ANY(sqlc.arg(external_ids)::text[])
  AND NOT (i.team_id = ANY(sqlc.arg(hidden_team_ids)::uuid[]));

-- name: ListItemsCreatedByImport :many
SELECT kind, record_id FROM import_items WHERE created_by = $1;

-- name: DeleteImportItemsForRecords :exec
DELETE FROM import_items WHERE workspace_id = $1 AND record_id = ANY(sqlc.arg(record_ids)::uuid[]);

-- name: CountForeignIssuesInTeam :one
-- Issues in the team that did not come from the given origin: if there are
-- none, imported issues can keep the numbers they had.
SELECT count(*) FROM issues i
WHERE i.team_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM import_items m
      WHERE m.workspace_id = i.workspace_id AND m.origin = $2 AND m.kind = 'issue' AND m.record_id = i.id
  );

-- name: IssueNumberTaken :one
SELECT EXISTS (SELECT 1 FROM issues WHERE team_id = $1 AND number = $2)::boolean;

-- name: ImportIssue :exec
-- An issue with its own past: dates and number as they were at the source.
INSERT INTO issues (id, workspace_id, team_id, number, title, description, state_id, priority, assignee_id, creator_id,
                    sort_order, label_ids, due_date, subscriber_ids, project_id, milestone_id, cycle_id,
                    created_at, updated_at, started_at, completed_at, archived_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22);

-- name: UpdateImportedIssue :exec
UPDATE issues
SET title = $2, description = $3, state_id = $4, priority = $5, assignee_id = $6, label_ids = $7,
    due_date = $8, project_id = $9, milestone_id = $10, cycle_id = $11,
    updated_at = $12, started_at = $13, completed_at = $14, archived_at = $15
WHERE id = $1;

-- name: SetIssueParentQuietly :exec
-- Without touching updated_at: the import is not an edit.
UPDATE issues SET parent_id = $2 WHERE id = $1;

-- name: DeleteIssueStateHistory :exec
DELETE FROM issue_state_history WHERE issue_id = $1;

-- name: InsertIssueStateHistories :batchexec
INSERT INTO issue_state_history (workspace_id, team_id, issue_id, state_id, state_type, entered_at, left_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ImportIssueActivities :batchexec
INSERT INTO issue_activities (id, workspace_id, issue_id, actor_id, type, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ImportComments :batchexec
INSERT INTO comments (id, workspace_id, issue_id, user_id, body, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateImportedComment :exec
UPDATE comments SET body = $2 WHERE id = $1;

-- name: UpdateImportedComments :batchexec
UPDATE comments SET body = $2 WHERE id = $1 AND body IS DISTINCT FROM $2;

-- name: ImportIssueRelation :exec
INSERT INTO issue_relations (id, workspace_id, issue_id, related_issue_id, type)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: ImportCycle :one
-- A cycle as it was. Returns no row if the team already has that number.
INSERT INTO cycles (id, workspace_id, team_id, number, name, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (team_id, number) DO NOTHING
RETURNING *;

-- name: RaiseNextIssueNumber :exec
-- After an import, new issues are numbered after the highest one there is.
UPDATE teams
SET next_issue_number = GREATEST(next_issue_number, (SELECT COALESCE(max(i.number), 0) + 1 FROM issues i WHERE i.team_id = teams.id))
WHERE teams.id = $1;

-- name: MaxWorkflowStatePosition :one
SELECT COALESCE(max(position), 0)::double precision FROM workflow_states WHERE team_id = $1;

-- name: ListTeamsOfWorkspace :many
SELECT * FROM teams WHERE workspace_id = $1 ORDER BY name;

-- name: ListWorkflowStatesOfWorkspace :many
SELECT * FROM workflow_states WHERE workspace_id = $1 ORDER BY team_id, position;

-- name: DeleteIssuesByIDs :exec
DELETE FROM issues WHERE workspace_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: DeleteCommentsByIDs :exec
DELETE FROM comments WHERE workspace_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListCommentIDsOfIssues :many
SELECT id FROM comments WHERE issue_id = ANY(sqlc.arg(issue_ids)::uuid[]);

-- name: DeleteProjectsByIDs :exec
-- Only those that no issue is in any more.
DELETE FROM projects p
WHERE p.workspace_id = $1 AND p.id = ANY(sqlc.arg(ids)::uuid[])
  AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.project_id = p.id);

-- name: DeleteMilestonesByIDs :exec
DELETE FROM milestones m
WHERE m.workspace_id = $1 AND m.id = ANY(sqlc.arg(ids)::uuid[])
  AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.milestone_id = m.id);

-- name: ListAttachmentsOfIssues :many
SELECT * FROM attachments WHERE issue_id = ANY(sqlc.arg(issue_ids)::uuid[]);

-- name: DeleteCyclesByIDs :exec
DELETE FROM cycles c
WHERE c.workspace_id = $1 AND c.id = ANY(sqlc.arg(ids)::uuid[])
  AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.cycle_id = c.id);

-- name: DeleteUnusedLabelsByIDs :exec
DELETE FROM labels l
WHERE l.workspace_id = $1 AND l.id = ANY(sqlc.arg(ids)::uuid[])
  AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.workspace_id = l.workspace_id AND l.id = ANY(i.label_ids));

-- name: DeleteUnusedStatesByIDs :exec
DELETE FROM workflow_states s
WHERE s.workspace_id = $1 AND s.id = ANY(sqlc.arg(ids)::uuid[])
  AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.state_id = s.id);

-- name: DeleteEmptyTeamsByIDs :exec
DELETE FROM teams t
WHERE t.workspace_id = $1 AND t.id = ANY(sqlc.arg(ids)::uuid[])
  AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.team_id = t.id);

-- name: ListAttachmentsByIDs :many
SELECT * FROM attachments WHERE workspace_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListAttachmentIDs :many
SELECT id FROM attachments WHERE workspace_id = $1;

-- name: SetIssueDescriptionQuietly :exec
-- Without touching updated_at: the import is not an edit.
UPDATE issues SET description = $2 WHERE id = $1;
