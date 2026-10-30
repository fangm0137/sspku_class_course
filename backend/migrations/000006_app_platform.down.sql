DROP TABLE tool_calls;
DROP TABLE llm_calls;
DROP TABLE mcp_tools;
DROP TABLE mcp_servers;

ALTER TABLE tutor_agents
    DROP FOREIGN KEY fk_agents_template,
    DROP INDEX uq_agents_builtin,
    DROP COLUMN builtin_key,
    DROP COLUMN prompt_template_id;

DROP TABLE prompt_templates;
