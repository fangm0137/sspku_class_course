# Spec Delta

## MODIFIED Requirements

### Requirement: Chunked index built during ingestion

系统 SHALL 在接受教师上传材料时，把提取文本切分为切片并持久化到 MySQL `knowledge_chunks`，与材料、知识库记录在同一事务内提交。切片 MUST 记录 `material_id`、`class_id`、`knowledge_entry_id`、材料内唯一的 `chunk_index`、相对**预处理后文本**的 `char_start`/`char_end`、`chunk_text`、`strategy` 与 `index_status`；`class_id` MUST 非空且与来源材料一致。切分 MUST NOT 改写 `knowledge_entries.body_text`，偏移 MUST 以预处理后文本为基准。

切分模式 SHALL 支持 `auto`（默认：目标 800 字节、重叠 80 字节，优先在空行、换行、句末标点处断开）、`custom`（显式 100–2000 字节、重叠不超过一半，可移除链接/邮箱或折叠连续空白）与 `hierarchy`（按 Markdown `#`/`##`/`###` 标题分章，标题保留在章节切片内）。字段命名 `char_start`/`char_end`/`strategy`/`index_status` 由 `add-traceable-vector-retrieval` 统一，取代早期的 `start_offset`/`end_offset`/`split_mode`/`embed_status`；语义不变，只有名称与 `strategy` 的取值集合发生变化。切片正文为空、或切片越界时 MUST 使上传失败并且不留下半成品。

改变策略 MUST 通过显式重建索引生效：先删除该材料的旧切片与旧向量，再按本次请求的策略重新写入。

#### Scenario: Upload produces offsets that map back to the original text

- **WHEN** 教师上传一份合法 txt 或 md 材料
- **THEN** 该材料的每个切片都写入 `knowledge_chunks`，切片序号从 0 连续递增，且用 `char_start`/`char_end` 截取该材料的知识库正文可逐字复现切片文本（未启用预处理时预处理文本即正文）

#### Scenario: Overlap and boundary handling

- **WHEN** 正文长度超过目标切片大小，包含空行、换行与句末标点
- **THEN** 相邻切片在切片大小允许时存在约定重叠，且切点优先落在空行或换行或句末标点，不产生空切片

#### Scenario: Chunking failure leaves no partial material

- **WHEN** 切分阶段发生确定失败（例如切片无法映射回正文）
- **THEN** 上传返回安全错误，材料、知识库与切片记录全部不提交，原文件按既有补偿策略清理
