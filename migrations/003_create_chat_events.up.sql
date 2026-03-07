CREATE TABLE IF NOT EXISTS chat_events (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    session_id      VARCHAR(36) NULL,
    phone_number    VARCHAR(20) NOT NULL,
    event_type      VARCHAR(50) NOT NULL,
    event_data      JSON NULL,
    state_from      VARCHAR(100) NULL,
    state_to        VARCHAR(100) NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_session (session_id),
    INDEX idx_phone (phone_number),
    INDEX idx_type_date (event_type, created_at),
    INDEX idx_created (created_at)
);
