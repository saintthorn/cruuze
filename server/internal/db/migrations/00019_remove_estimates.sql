-- +goose Up
ALTER TABLE issues DROP COLUMN estimate;
DELETE FROM issue_activities WHERE type = 'estimate';

-- Remove obsolete fields and activities from events retained for offline clients.
UPDATE sync_actions SET data = data - 'estimate'
WHERE model = 'Issue' AND data ? 'estimate';
DELETE FROM sync_actions WHERE model = 'Activity' AND data->>'type' = 'estimate';

-- +goose Down
-- Removed values and activity history cannot be restored.
ALTER TABLE issues ADD COLUMN estimate smallint CHECK (estimate BETWEEN 0 AND 100);
