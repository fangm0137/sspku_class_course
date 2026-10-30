-- 个人项目验收：多应用、提示词模板、MCP 外部工具与模型调用审计。
-- 四张新表都按班级切分，并用复合外键把"同班"写进数据库；
-- 审计两张表不挂业务外键：会话被删除后证据必须留下来。

CREATE TABLE prompt_templates (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(64) NOT NULL,
    -- 模板体含 {{app_name}}/{{today}}/{{question}} 三个占位符，由服务端渲染一次。
    body TEXT NOT NULL,
    description VARCHAR(255) NULL,
    -- 停用只影响"新建引用"，已引用的应用仍按它渲染。
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_templates_class_name (class_id, name),
    -- 复合唯一键供 tutor_agents 做同班外键：跨班引用模板在存储层即不可能成立。
    UNIQUE KEY uq_templates_id_class (id, class_id),
    CONSTRAINT fk_templates_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT ck_templates_body CHECK (CHAR_LENGTH(body) BETWEEN 1 AND 4000),
    CONSTRAINT ck_templates_name CHECK (CHAR_LENGTH(name) BETWEEN 1 AND 64)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 应用加两列：模板引用可为空（回落到自由文本），内置标识用于幂等初始化。
ALTER TABLE tutor_agents
    ADD COLUMN prompt_template_id BIGINT UNSIGNED NULL AFTER prompt,
    ADD COLUMN builtin_key VARCHAR(64) NULL AFTER prompt_template_id,
    ADD UNIQUE KEY uq_agents_builtin (builtin_key),
    ADD CONSTRAINT fk_agents_template FOREIGN KEY (prompt_template_id, class_id)
        REFERENCES prompt_templates (id, class_id),
    ADD CONSTRAINT ck_agents_builtin CHECK (builtin_key IS NULL OR CHAR_LENGTH(builtin_key) BETWEEN 1 AND 64);

CREATE TABLE mcp_servers (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(64) NOT NULL,
    base_url VARCHAR(255) NOT NULL,
    -- 令牌密文：AES-GCM(每台服务器各用各的令牌)，密钥由 TOKEN_SECRET 派生。接口只回 has_token。
    auth_token_cipher VARBINARY(512) NULL,
    status ENUM('disconnected','connected','error') NOT NULL DEFAULT 'disconnected',
    last_error VARCHAR(255) NULL,
    connected_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_mcp_class_name (class_id, name),
    UNIQUE KEY uq_mcp_id_class (id, class_id),
    CONSTRAINT fk_mcp_servers_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT ck_mcp_servers_name CHECK (CHAR_LENGTH(name) BETWEEN 1 AND 64),
    CONSTRAINT ck_mcp_servers_url CHECK (CHAR_LENGTH(base_url) BETWEEN 1 AND 255)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 工具清单缓存：连接时整体替换（先删后插），断开时清空。
CREATE TABLE mcp_tools (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    server_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(64) NOT NULL,
    description VARCHAR(1000) NOT NULL DEFAULT '',
    -- 发现结果原样保存，运行期既作声明也作参数校验依据。
    input_schema JSON NOT NULL,
    discovered_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_mcp_tool (server_id, name),
    KEY idx_mcp_tools_class (class_id, name),
    CONSTRAINT fk_mcp_tool_server FOREIGN KEY (server_id, class_id)
        REFERENCES mcp_servers (id, class_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 模型调用审计：工具循环会在一次提问里产生多条，round_no 区分。
CREATE TABLE llm_calls (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    conversation_id BIGINT UNSIGNED NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    agent_id BIGINT UNSIGNED NULL,
    purpose ENUM('answer') NOT NULL DEFAULT 'answer',
    round_no INT NOT NULL DEFAULT 1,
    model VARCHAR(64) NOT NULL DEFAULT '',
    -- 真正发出的字节，不重建、不摘要。
    request_json MEDIUMTEXT NOT NULL,
    -- 派生列：验收时一条 SELECT 就能看到，不必解析 JSON。
    system_prompt MEDIUMTEXT NOT NULL,
    user_prompt MEDIUMTEXT NOT NULL,
    message_count INT NOT NULL,
    context_chars INT NOT NULL,
    skill_name VARCHAR(32) NULL,
    tool_def_count INT NOT NULL DEFAULT 0,
    tool_call_count INT NOT NULL DEFAULT 0,
    finish_reason VARCHAR(32) NULL,
    response_text MEDIUMTEXT NULL,
    response_json MEDIUMTEXT NULL,
    http_status INT NOT NULL,
    status ENUM('ok','failed') NOT NULL,
    error_text VARCHAR(255) NULL,
    prompt_tokens INT NULL,
    completion_tokens INT NULL,
    latency_ms INT NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_llm_calls_class (class_id, created_at),
    KEY idx_llm_calls_user (user_id, created_at),
    KEY idx_llm_calls_conversation (conversation_id, round_no),
    CONSTRAINT fk_llm_calls_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT fk_llm_calls_user FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT ck_llm_calls_round CHECK (round_no >= 1),
    CONSTRAINT ck_llm_calls_status CHECK (status IN ('ok','failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;

-- 工具调用审计：调什么、成没成、谁触发、哪轮、何时，各对应一列。
CREATE TABLE tool_calls (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    class_id BIGINT UNSIGNED NOT NULL,
    conversation_id BIGINT UNSIGNED NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    llm_call_id BIGINT UNSIGNED NULL,
    round_no INT NOT NULL,
    server_id BIGINT UNSIGNED NULL,
    server_name VARCHAR(64) NOT NULL,
    tool_name VARCHAR(64) NOT NULL,
    arguments_json TEXT NOT NULL,
    status ENUM('ok','denied','error') NOT NULL,
    result_excerpt VARCHAR(500) NULL,
    error_text VARCHAR(255) NULL,
    latency_ms INT NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_tool_calls_conversation (conversation_id, round_no),
    KEY idx_tool_calls_class (class_id, created_at),
    CONSTRAINT fk_tool_calls_class FOREIGN KEY (class_id) REFERENCES classes (id),
    CONSTRAINT fk_tool_calls_user FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT ck_tool_calls_status CHECK (status IN ('ok','denied','error'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
