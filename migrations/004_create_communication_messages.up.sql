CREATE TABLE IF NOT EXISTS communication_messages (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    session_id      VARCHAR(36) NULL,
    phone_number    VARCHAR(20) NOT NULL,
    direction       ENUM('inbound','outbound') NOT NULL,
    message_type    VARCHAR(20) NOT NULL,
    content         TEXT NULL,
    bird_message_id VARCHAR(100) NULL,
    status          VARCHAR(20) DEFAULT 'sent',
    error_message   TEXT NULL,
    retry_count     INT NOT NULL DEFAULT 0,
    last_retry_at   TIMESTAMP NULL,
    sent_at         TIMESTAMP NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_session (session_id),
    INDEX idx_phone_dir (phone_number, direction),
    INDEX idx_bird_id (bird_message_id)
);
