ALTER TABLE knowledge_chunks DROP FOREIGN KEY fk_chunks_knowledge_entry;
ALTER TABLE knowledge_chunks DROP CHECK ck_chunks_offsets;

-- hierarchy 是本次新引入的策略，回退时必须先把它归一化，否则改回 ENUM('auto','custom') 会失败。
UPDATE knowledge_chunks SET strategy = 'auto' WHERE strategy = 'hierarchy';
UPDATE knowledge_chunks SET index_status = 'pending' WHERE index_status NOT IN ('pending','ready','failed');

ALTER TABLE knowledge_chunks
    CHANGE COLUMN char_start start_offset INT UNSIGNED NOT NULL,
    CHANGE COLUMN char_end end_offset INT UNSIGNED NOT NULL,
    CHANGE COLUMN strategy split_mode ENUM('auto','custom') NOT NULL,
    CHANGE COLUMN index_status embed_status ENUM('pending','ready','failed') NOT NULL DEFAULT 'pending';

ALTER TABLE knowledge_chunks RENAME INDEX idx_chunks_index_status TO idx_chunks_embed_status;

ALTER TABLE knowledge_chunks MODIFY COLUMN knowledge_entry_id BIGINT UNSIGNED NULL;
ALTER TABLE knowledge_chunks DROP COLUMN knowledge_entry_id;

ALTER TABLE knowledge_chunks ADD CONSTRAINT ck_chunks_offsets CHECK (end_offset > start_offset);
