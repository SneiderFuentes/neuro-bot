CREATE TABLE IF NOT EXISTS message_inbox (
    id          VARCHAR(100) PRIMARY KEY,
    phone       VARCHAR(20) NOT NULL,
    raw_body    TEXT NOT NULL,
    msg_type    VARCHAR(20) NOT NULL DEFAULT 'inbound',
    status      VARCHAR(20) NOT NULL DEFAULT 'pending',
    received_at TIMESTAMP NOT NULL,
    processed_at TIMESTAMP NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_inbox_status (status),
    INDEX idx_inbox_created (created_at)
);
