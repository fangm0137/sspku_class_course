-- Retrieval需要按切片检索，因此正文在入库时切分并单独保存。
-- 偏移以 knowledge_entries.extracted_text（去掉首个 BOM 的正文）为基准，使用字节偏移，
-- 因此 start/end 截取正文可逐字复现 chunk_text，引用可以回溯到原文位置。
CREATE TABLE knowledge_chunks (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    material_id BIGINT UNSIGNED NOT NULL,
    class_id BIGINT UNSIGNED NOT NULL,
    chunk_index INT UNSIGNED NOT NULL,
    start_offset INT UNSIGNED NOT NULL,
    end_offset INT UNSIGNED NOT NULL,
    chunk_text LONGTEXT NOT NULL,
    split_mode ENUM('auto','custom') NOT NULL,
    -- 向量写入是外部调用，可能失败；状态让"切片已入库但还没向量"可被区分。
    embed_status ENUM('pending','ready','failed') NOT NULL DEFAULT 'pending',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_chunks_material_index (material_id, chunk_index),
    KEY idx_chunks_class_id (class_id),
    KEY idx_chunks_embed_status (embed_status),
    CONSTRAINT fk_chunks_material_class FOREIGN KEY (material_id, class_id) REFERENCES materials (id, class_id),
    CONSTRAINT ck_chunks_offsets CHECK (end_offset > start_offset),
    FULLTEXT KEY ft_chunks_text (chunk_text) WITH PARSER ngram
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
