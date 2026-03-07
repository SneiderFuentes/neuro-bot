ALTER TABLE sessions
    ADD COLUMN escalated_at TIMESTAMP NULL AFTER conversation_id,
    ADD COLUMN escalated_team VARCHAR(100) NULL AFTER escalated_at,
    ADD COLUMN resumed_at TIMESTAMP NULL AFTER escalated_team,
    ADD INDEX idx_escalated (status, escalated_at);
