CREATE TABLE IF NOT EXISTS medication_pending (
    id                 INT AUTO_INCREMENT PRIMARY KEY,
    cedula             VARCHAR(30)  NOT NULL,
    paciente           VARCHAR(200) NOT NULL,
    eps                VARCHAR(100) NOT NULL DEFAULT '',
    medicamento        VARCHAR(300) NOT NULL,
    nombre_comercial   VARCHAR(200) NOT NULL DEFAULT '',
    concentracion      VARCHAR(100) NOT NULL DEFAULT '',
    forma_farmaceutica VARCHAR(100) NOT NULL DEFAULT '',
    saldo_actual       INT          NOT NULL DEFAULT 0,
    fecha_vencimiento  VARCHAR(50)  NOT NULL DEFAULT '',
    activo             TINYINT(1)   NOT NULL DEFAULT 1,
    created_at         DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_cedula (cedula),
    INDEX idx_activo_cedula (activo, cedula)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
