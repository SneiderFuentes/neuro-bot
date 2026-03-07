CREATE TABLE IF NOT EXISTS waiting_list (
    id                   VARCHAR(36) PRIMARY KEY,
    phone_number         VARCHAR(20) NOT NULL,
    patient_id           VARCHAR(20) NOT NULL,
    patient_doc          VARCHAR(20) NOT NULL,
    patient_name         VARCHAR(200) NOT NULL,
    patient_age          INT NOT NULL,
    patient_gender       CHAR(1) NOT NULL,
    patient_entity       VARCHAR(20) NOT NULL,

    cups_code            VARCHAR(20) NOT NULL,
    cups_name            VARCHAR(200) NOT NULL,
    is_contrasted        TINYINT(1) NOT NULL DEFAULT 0,
    is_sedated           TINYINT(1) NOT NULL DEFAULT 0,
    espacios             INT NOT NULL DEFAULT 1,
    procedures_json      JSON NOT NULL,

    gfr_creatinine       DECIMAL(5,2) NULL,
    gfr_height_cm        INT NULL,
    gfr_weight_kg        DECIMAL(5,1) NULL,
    gfr_disease_type     VARCHAR(20) NULL,
    gfr_calculated       DECIMAL(6,1) NULL,

    is_pregnant          TINYINT(1) NULL,
    baby_weight_cat      VARCHAR(10) NULL,
    preferred_doctor_doc VARCHAR(20) NULL,

    status               ENUM('waiting','notified','scheduled','declined','expired','duplicate_found') DEFAULT 'waiting',
    notified_at          TIMESTAMP NULL,
    last_notified_at     TIMESTAMP NULL,
    resolved_at          TIMESTAMP NULL,
    created_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at           TIMESTAMP NOT NULL,

    INDEX idx_cups_status (cups_code, status),
    INDEX idx_patient_cups (patient_id, cups_code),
    INDEX idx_phone (phone_number),
    INDEX idx_status_created (status, created_at),
    INDEX idx_expires (expires_at)
);
