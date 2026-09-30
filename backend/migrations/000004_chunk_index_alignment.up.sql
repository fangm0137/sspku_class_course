-- 000003 建表时用的是内部命名；第4课规约把字段固定为 char_start/char_end/strategy/index_status
-- 并额外要求 knowledge_entry_id（向量 payload 与回表都用它做标识）。本迁移只做对齐，
-- 不改动任何既有业务的语义。
--
-- 偏移基准：char_start/char_end 是**预处理后文本**的字节区间；启用移除链接/邮箱或折叠空白时
-- 它不再是原始文件的下标，未启用预处理时与 body_text 完全一致。

ALTER TABLE knowledge_chunks DROP CHECK ck_chunks_offsets;

ALTER TABLE knowledge_chunks
    CHANGE COLUMN start_offset char_start INT UNSIGNED NOT NULL
        COMMENT '切片在预处理后正文中的起始字节偏移（含）',
    CHANGE COLUMN end_offset char_end INT UNSIGNED NOT NULL
        COMMENT '切片在预处理后正文中的结束字节偏移（不含），char_end > char_start',
    CHANGE COLUMN split_mode strategy ENUM('auto','custom','hierarchy') NOT NULL
        COMMENT '本次建索引使用的切分策略',
    CHANGE COLUMN embed_status index_status ENUM('pending','ready','failed') NOT NULL DEFAULT 'pending'
        COMMENT '检索索引状态：ready 可被检索；pending 等待写入向量；failed 建索引失败';

ALTER TABLE knowledge_chunks RENAME INDEX idx_chunks_embed_status TO idx_chunks_index_status;

-- knowledge_entry_id 是切片的正文来源，回表与 payload 都以它为准。
ALTER TABLE knowledge_chunks ADD COLUMN knowledge_entry_id BIGINT UNSIGNED NULL AFTER class_id;

-- 历史切片按 material_id 回填；knowledge_entries 对 material_id 有唯一键，因此结果唯一。
UPDATE knowledge_chunks c
JOIN knowledge_entries k ON k.material_id = c.material_id
SET c.knowledge_entry_id = k.id
WHERE c.knowledge_entry_id IS NULL;

-- 没有正文来源的切片既不能回溯也不能回表，保留它只会让检索返回无法解释的命中，故删除。
DELETE FROM knowledge_chunks WHERE knowledge_entry_id IS NULL;

ALTER TABLE knowledge_chunks MODIFY COLUMN knowledge_entry_id BIGINT UNSIGNED NOT NULL;

ALTER TABLE knowledge_chunks
    ADD CONSTRAINT ck_chunks_offsets CHECK (char_end > char_start),
    ADD CONSTRAINT fk_chunks_knowledge_entry FOREIGN KEY (knowledge_entry_id) REFERENCES knowledge_entries (id);
