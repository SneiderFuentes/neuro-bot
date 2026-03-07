CREATE TABLE IF NOT EXISTS notification_pending (
    phone           VARCHAR(20) PRIMARY KEY,
    type            VARCHAR(20) NOT NULL,
    appointment_id  VARCHAR(50) NULL,
    waiting_list_id VARCHAR(36) NULL,
    bird_message_id VARCHAR(100) NULL,
    conversation_id VARCHAR(100) NULL,
    retry_count     INT NOT NULL DEFAULT 0,
    expires_at      TIMESTAMP NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_type (type),
    INDEX idx_expires (expires_at)
);
