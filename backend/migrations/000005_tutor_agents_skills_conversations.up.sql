-- 第 5 课：智能体、技能、会话与消息。
-- 全部按班级切分，并用复合外键把"同班"写进数据库，使跨班绑定在存储层即不可能成立。

CREATE TABLE tutor_agents (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(64) NOT NULL,
    -- 提示词为空是合法状态：回答路径回退到服务端默认角色设定。
    prompt VARCHAR(2000) NOT NULL DEFAULT '',
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_agents_class_name (class_id, name),
    -- 复合唯一键供会话与绑定表做同班外键。
    UNIQUE KEY uq_agents_id_class (id, class_id),
    KEY idx_agents_class_enabled (class_id, enabled),
    CONSTRAINT fk_agents_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT ck_agents_name CHECK (CHAR_LENGTH(name) BETWEEN 1 AND 64),
    CONSTRAINT ck_agents_prompt CHECK (CHAR_LENGTH(prompt) <= 2000)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE skills (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(32) NOT NULL,
    trigger_text VARCHAR(120) NOT NULL,
    description VARCHAR(2000) NOT NULL,
    -- 工具列表只允许 knowledge_search；内容由服务端校验，这里约束形状。
    tools_json JSON NOT NULL,
    source_text TEXT NOT NULL,
    subject_text VARCHAR(32) NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    seed_key VARCHAR(64) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_skills_class_name (class_id, name),
    UNIQUE KEY uq_skills_id_class (id, class_id),
    -- 预置技能用 seed_key 做幂等键；教师自建技能该列为 NULL（唯一索引允许多个 NULL）。
    UNIQUE KEY uq_skills_seed_key (seed_key),
    KEY idx_skills_class_enabled (class_id, enabled),
    CONSTRAINT fk_skills_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT ck_skills_name CHECK (CHAR_LENGTH(name) BETWEEN 1 AND 32),
    CONSTRAINT ck_skills_trigger CHECK (CHAR_LENGTH(trigger_text) BETWEEN 1 AND 120),
    CONSTRAINT ck_skills_description CHECK (CHAR_LENGTH(description) BETWEEN 1 AND 2000),
    CONSTRAINT ck_skills_source CHECK (CHAR_LENGTH(source_text) BETWEEN 1 AND 8000),
    CONSTRAINT ck_skills_subject CHECK (subject_text IS NULL OR CHAR_LENGTH(subject_text) BETWEEN 1 AND 32),
    CONSTRAINT ck_skills_tools CHECK (JSON_TYPE(tools_json) = 'ARRAY')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 绑定关系用两张关联表表达，不做行内快照：教师改绑定，下一次提问即按新关系作答。
CREATE TABLE tutor_agent_skills (
    agent_id BIGINT UNSIGNED NOT NULL,
    skill_id BIGINT UNSIGNED NOT NULL,
    class_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (agent_id, skill_id),
    KEY idx_agent_skills_skill (skill_id),
    CONSTRAINT fk_agent_skills_agent FOREIGN KEY (agent_id, class_id) REFERENCES tutor_agents (id, class_id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_skills_skill FOREIGN KEY (skill_id, class_id) REFERENCES skills (id, class_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE tutor_agent_materials (
    agent_id BIGINT UNSIGNED NOT NULL,
    material_id BIGINT UNSIGNED NOT NULL,
    class_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (agent_id, material_id),
    KEY idx_agent_materials_material (material_id),
    CONSTRAINT fk_agent_materials_agent FOREIGN KEY (agent_id, class_id) REFERENCES tutor_agents (id, class_id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_materials_material FOREIGN KEY (material_id, class_id) REFERENCES materials (id, class_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 会话是容器：只记归属、选定的智能体与标题；对话正文与出处都在 messages。
CREATE TABLE conversations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    agent_id BIGINT UNSIGNED NOT NULL,
    title VARCHAR(64) NOT NULL DEFAULT '新的提问',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_conversations_user (user_id, updated_at),
    KEY idx_conversations_class (class_id),
    CONSTRAINT fk_conversations_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT fk_conversations_user FOREIGN KEY (user_id) REFERENCES users (id),
    -- 同班复合外键：会话不可能指向他班智能体。
    CONSTRAINT fk_conversations_agent FOREIGN KEY (agent_id, class_id) REFERENCES tutor_agents (id, class_id),
    CONSTRAINT ck_conversations_title CHECK (CHAR_LENGTH(title) BETWEEN 1 AND 64)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

CREATE TABLE messages (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    conversation_id BIGINT UNSIGNED NOT NULL,
    role ENUM('user','assistant') NOT NULL,
    content MEDIUMTEXT NOT NULL,
    -- 出处的引用列表与本次命中的技能名：只有助手消息有。
    citations_json JSON NULL,
    skill_name VARCHAR(32) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_messages_conversation (conversation_id, id),
    CONSTRAINT fk_messages_conversation FOREIGN KEY (conversation_id) REFERENCES conversations (id) ON DELETE CASCADE,
    CONSTRAINT ck_messages_role CHECK (role IN ('user','assistant')),
    CONSTRAINT ck_messages_aux CHECK (role = 'assistant' OR (citations_json IS NULL AND skill_name IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
