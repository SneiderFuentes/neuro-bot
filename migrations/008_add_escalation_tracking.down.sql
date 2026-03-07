ALTER TABLE sessions
    DROP INDEX idx_escalated,
    DROP COLUMN resumed_at,
    DROP COLUMN escalated_team,
    DROP COLUMN escalated_at;
