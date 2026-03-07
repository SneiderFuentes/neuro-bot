CREATE TABLE IF NOT EXISTS sessions (
    id               VARCHAR(36) PRIMARY KEY,
    phone_number     VARCHAR(20) NOT NULL,
    current_state    VARCHAR(100) NOT NULL DEFAULT 'CHECK_BUSINESS_HOURS',
    status           ENUM('active','completed','abandoned','escalated') DEFAULT 'active',
    menu_option      VARCHAR(20) NULL,
    patient_id       VARCHAR(20) NULL,
    patient_doc      VARCHAR(20) NULL,
    patient_name     VARCHAR(200) NULL,
    patient_age      INT NULL,
    patient_gender   CHAR(1) NULL,
    patient_entity   VARCHAR(20) NULL,
    retry_count      INT NOT NULL DEFAULT 0,
    conversation_id  VARCHAR(100) NULL,
    last_activity_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at       TIMESTAMP NOT NULL,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_phone_status (phone_number, status),
    INDEX idx_expires (expires_at),
    INDEX idx_status (status)
);
