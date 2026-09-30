-- Restores the exact 000001 definition so the schema can move back to the
-- cookie-session iteration. No row data is recovered: the table only ever held
-- short lived anonymous or logged in session identifiers.
CREATE TABLE IF NOT EXISTS sessions (
    session_id_hash BINARY(32) NOT NULL,
    user_id BIGINT UNSIGNED NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at DATETIME(6) NOT NULL,
    csrf_token_hash BINARY(32) NULL,
    csrf_expires_at DATETIME(6) NULL,
    PRIMARY KEY (session_id_hash),
    KEY idx_sessions_user_id (user_id),
    KEY idx_sessions_expires_at (expires_at),
    CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT ck_sessions_expiry CHECK (expires_at > created_at),
    CONSTRAINT ck_sessions_csrf CHECK (
        (csrf_token_hash IS NULL AND csrf_expires_at IS NULL) OR
        (csrf_token_hash IS NOT NULL AND csrf_expires_at IS NOT NULL AND csrf_expires_at <= expires_at)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
