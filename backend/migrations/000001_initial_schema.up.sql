CREATE TABLE classes (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(128) NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE users (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    username VARCHAR(64) NOT NULL,
    password_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    role ENUM('teacher','student') NOT NULL,
    class_id BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_users_username (username),
    KEY idx_users_class_id (class_id),
    CONSTRAINT fk_users_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT ck_users_role CHECK (role IN ('teacher','student')),
    CONSTRAINT ck_users_password_hash CHECK (password_hash LIKE '$scrypt$%' AND CHAR_LENGTH(password_hash) >= 60)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE sessions (
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

CREATE TABLE materials (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    uploader_id BIGINT UNSIGNED NULL,
    original_name VARCHAR(255) NOT NULL,
    storage_key VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    size BIGINT UNSIGNED NOT NULL,
    source ENUM('seed','upload') NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_materials_id_class (id, class_id),
    UNIQUE KEY uq_materials_storage_key (storage_key),
    KEY idx_materials_class_id (class_id),
    KEY idx_materials_uploader_id (uploader_id),
    CONSTRAINT fk_materials_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT fk_materials_uploader FOREIGN KEY (uploader_id) REFERENCES users (id),
    CONSTRAINT ck_materials_source CHECK (source IN ('seed','upload')),
    CONSTRAINT ck_materials_upload_actor CHECK (source = 'seed' OR uploader_id IS NOT NULL)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE knowledge_entries (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    material_id BIGINT UNSIGNED NOT NULL,
    class_id BIGINT UNSIGNED NOT NULL,
    extracted_text LONGTEXT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_knowledge_material_id (material_id),
    KEY idx_knowledge_class_id (class_id),
    KEY idx_knowledge_material_class (material_id, class_id),
    CONSTRAINT fk_knowledge_material_class FOREIGN KEY (material_id, class_id) REFERENCES materials (id, class_id),
    CONSTRAINT fk_knowledge_class FOREIGN KEY (class_id) REFERENCES classes (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
