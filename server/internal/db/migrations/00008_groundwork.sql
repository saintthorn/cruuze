-- +goose Up

-- When work on an issue began and when it ended, kept by a trigger so that
-- every way of changing an issue's state is covered. started_at is the first
-- time it entered a "started" state and never moves; completed_at is set
-- while the issue is completed or canceled and cleared if it is reopened.
ALTER TABLE issues
    ADD COLUMN started_at   timestamptz,
    ADD COLUMN completed_at timestamptz;

-- One row per stay of an issue in a state: what reports, forecasts and time
-- in progress are computed from.
CREATE TABLE issue_state_history (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    team_id      uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    issue_id     uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    -- NULL once the state itself has been deleted; its type is kept.
    state_id     uuid REFERENCES workflow_states (id) ON DELETE SET NULL,
    state_type   text NOT NULL,
    entered_at   timestamptz NOT NULL,
    -- NULL while the issue is still in this state.
    left_at      timestamptz
);
CREATE INDEX issue_state_history_issue_idx ON issue_state_history (issue_id, entered_at);
CREATE INDEX issue_state_history_team_idx ON issue_state_history (team_id, entered_at);

-- Rebuild the history of existing issues from their activity feed: the state
-- each was created in, then every recorded change.
WITH changes AS (
    SELECT a.issue_id, a.created_at AS at, a.to_value::uuid AS state_id
    FROM issue_activities a
    WHERE a.type = 'state' AND a.to_value IS NOT NULL
    UNION ALL
    SELECT i.id, i.created_at,
           COALESCE((SELECT a.from_value::uuid FROM issue_activities a
                     WHERE a.issue_id = i.id AND a.type = 'state' AND a.from_value IS NOT NULL
                     ORDER BY a.created_at LIMIT 1), i.state_id)
    FROM issues i
), stays AS (
    SELECT c.issue_id, c.state_id, c.at, lead(c.at) OVER (PARTITION BY c.issue_id ORDER BY c.at) AS next_at
    FROM changes c
)
INSERT INTO issue_state_history (workspace_id, team_id, issue_id, state_id, state_type, entered_at, left_at)
SELECT i.workspace_id, i.team_id, i.id, s.id, s.type, stays.at, stays.next_at
FROM stays
JOIN issues i ON i.id = stays.issue_id
JOIN workflow_states s ON s.id = stays.state_id;

UPDATE issues i
SET started_at = (SELECT min(h.entered_at) FROM issue_state_history h WHERE h.issue_id = i.id AND h.state_type = 'started'),
    completed_at = (SELECT max(h.entered_at) FROM issue_state_history h
                    WHERE h.issue_id = i.id AND h.left_at IS NULL AND h.state_type IN ('completed', 'canceled'));

-- +goose StatementBegin
CREATE FUNCTION issues_stamp_state() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    new_type text;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.state_id = OLD.state_id THEN
        RETURN NEW;
    END IF;
    SELECT type INTO new_type FROM workflow_states WHERE id = NEW.state_id;
    IF new_type = 'started' AND NEW.started_at IS NULL THEN
        NEW.started_at := now();
    END IF;
    IF new_type IN ('completed', 'canceled') THEN
        NEW.completed_at := COALESCE(NEW.completed_at, now());
    ELSE
        NEW.completed_at := NULL;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION issues_log_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.state_id = OLD.state_id THEN
            RETURN NULL;
        END IF;
        UPDATE issue_state_history SET left_at = now() WHERE issue_id = NEW.id AND left_at IS NULL;
    END IF;
    INSERT INTO issue_state_history (workspace_id, team_id, issue_id, state_id, state_type, entered_at)
    SELECT NEW.workspace_id, NEW.team_id, NEW.id, s.id, s.type, now()
    FROM workflow_states s WHERE s.id = NEW.state_id;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER issues_stamp_state BEFORE INSERT OR UPDATE OF state_id ON issues
    FOR EACH ROW EXECUTE FUNCTION issues_stamp_state();
CREATE TRIGGER issues_log_state AFTER INSERT OR UPDATE OF state_id ON issues
    FOR EACH ROW EXECUTE FUNCTION issues_log_state();

-- +goose Down
DROP TRIGGER issues_log_state ON issues;
DROP TRIGGER issues_stamp_state ON issues;
DROP FUNCTION issues_log_state();
DROP FUNCTION issues_stamp_state();
DROP TABLE issue_state_history;
ALTER TABLE issues DROP COLUMN completed_at, DROP COLUMN started_at;
