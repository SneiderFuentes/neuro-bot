CREATE TABLE IF NOT EXISTS communication_calls (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    phone_number    VARCHAR(20) NOT NULL,
    call_type       ENUM('ivr_reminder') NOT NULL,
    status          VARCHAR(20) DEFAULT 'initiated',
    bird_call_id    VARCHAR(100) NULL,
    duration_sec    INT NULL,
    result          VARCHAR(50) NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_phone (phone_number),
    INDEX idx_status (status),
    INDEX idx_created (created_at)
);
