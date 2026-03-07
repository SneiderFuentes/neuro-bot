CREATE TABLE IF NOT EXISTS session_context (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    session_id  VARCHAR(36) NOT NULL,
    ctx_key     VARCHAR(100) NOT NULL,
    ctx_value   TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    UNIQUE INDEX idx_session_key (session_id, ctx_key),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
