-- Compound index for chat_events: subquery by session_id + ORDER BY created_at
ALTER TABLE chat_events ADD INDEX idx_session_created (session_id, created_at);

-- Index for notification_pending: WHERE expires_at < NOW()
ALTER TABLE notification_pending ADD INDEX idx_expires_at (expires_at);
