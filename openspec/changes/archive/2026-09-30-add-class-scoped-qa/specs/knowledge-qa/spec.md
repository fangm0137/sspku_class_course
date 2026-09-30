# Spec Delta

## Purpose

让 CampusClaw 的登录用户在**本班范围内**就材料正文提问，并得到带可回溯出处的简短回答：正文在入库时切分为切片，检索按会话班级过滤，生成回答只用命中的切片，没有依据时如实说明而不调用模型。

## ADDED Requirements

### Requirement: Chunked index built during ingestion
系统 SHALL 在接受教师上传材料时，把提取文本切分为切片并持久化到 MySQL `knowledge_chunks`，与材料、知识库记录在同一事务内提交。切片 MUST 记录 `material_id`、`class_id`、材料内唯一的 `chunk_index`、相对**预处理后文本**的 `start_offset`/`end_offset`、`chunk_text`、`split_mode` 与 `embed_status`；`class_id` MUST 非空且与来源材料一致。切分 MUST NOT 改写 `knowledge_entries.extracted_text`，偏移 MUST 以预处理后文本为基准。

切分模式 SHALL 支持 `auto`（默认：目标 800 个字符、重叠 80，优先在空行、换行、句末标点处断开）与 `custom`（显式 100–2000 个字符、重叠 0–50%）。切片正文为空、或切片越界时 MUST 使上传失败并且不留下半成品。

#### Scenario: Upload produces offsets that map back to the original text
- **WHEN** 教师上传一份合法 txt 或 md 材料
- **THEN** 该材料的每个切片都写入 `knowledge_chunks`，切片序号从 0 连续递增，且用 `start_offset`/`end_offset` 截取该材料的知识库正文可逐字复现切片文本

#### Scenario: Overlap and boundary handling
- **WHEN** 正文长度超过目标切片大小，包含空行、换行与句末标点
- **THEN** 相邻切片在切片大小允许时存在约定重叠，且切点优先落在空行或换行或句末标点，不产生空切片

#### Scenario: Chunking failure leaves no partial material
- **WHEN** 切分阶段发生确定失败（例如切片无法映射回正文）
- **THEN** 上传返回安全错误，材料、知识库与切片记录全部不提交，原文件按既有补偿策略清理

### Requirement: Class scoped retrieval
系统 SHALL 只检索调用者所属班级的切片，班级 MUST 来自服务端可信账号记录，请求体或查询串中的班级参数 SHALL 被忽略。检索 SHALL 支持两条可独立启用的路径：

- **关键字**：使用 MySQL 8.0 的 `FULLTEXT ... WITH PARSER ngram`（`ngram_token_size = 2`），MUST NOT 依赖外部服务。
- **向量**：把问句送入嵌入网关取得向量，在 Qdrant 集合中按 `class_id` 过滤后按余弦距离检索，再按切片主键回 MySQL 取正文。

两条路径都可用时 SHALL 按名次做 RRF 融合（`k = 60`）得到最终顺序。融合后的候选 MUST 至少满足其中之一：关键字命中，或余弦相似度不低于 0.35。向量路径在嵌入网关未配置时 SHALL 自动停用而不使问答整体失败；在用的路径因依赖不可用而无法完成、并且**没有任何其他路径产出候选**时 MUST 返回 503，不得把依赖故障说成"资料里没有"。

#### Scenario: Retrieval never crosses classes
- **WHEN** 学生 B1 的班级没有相关材料，而 A 班存在包含答案的材料
- **THEN** 检索结果为空，返回体不包含任何 A 班的材料标题、切片文本或偏移

#### Scenario: Keyword path works without any model gateway
- **WHEN** 未配置嵌入网关，用户就本班材料中确实出现的关键词提问
- **THEN** 关键字路径仍返回命中切片，问答可以继续

#### Scenario: Vector path activates when embeddings are configured
- **WHEN** 配置了嵌入网关与 Qdrant，且材料入库时已完成向量写入
- **THEN** 检索会额外走向量路径，并按 RRF 融合两条路径的名次

#### Scenario: Dependency failure is reported honestly
- **WHEN** 在用的检索路径依赖不可用（例如 Qdrant 停机），且关键字路径同样没有候选
- **THEN** 问答返回 503 `service_unavailable`，不返回空命中冒充"资料里没有"；若关键字路径已有候选，则继续据此作答

### Requirement: Grounded answers with traceable citations
系统 SHALL 提供 `POST /api/ask`，请求体为 `{"question":"<问题>"}`。处理顺序 MUST 为先检索后生成：取融合排序后的前 4 条切片；**没有命中时直接返回固定文案说明资料中未找到相关内容，且 MUST NOT 调用对话网关**。

有命中时 SHALL 只把命中的切片作为依据交给对话网关，要求生成简短回答并用 `[1]`、`[2]` 标注对应出处；返回体的每个引用 MUST 含材料标题、切片序号、字符区间与摘录，摘录 MUST 取自 MySQL 的切片正文。班级与身份 MUST 只来自会话。问题为空或全为空白 MUST 返回 400。

#### Scenario: Answer cites the retrieved chunks
- **WHEN** 用户就本班材料中确有答案的问题提问
- **THEN** 返回 200，回答文本含 `[n]` 标记，且每个标记都能在返回的引用数组里找到对应的材料标题、切片序号与字符区间

#### Scenario: No hits means no model call
- **WHEN** 本班材料里没有与问题相关的内容
- **THEN** 返回 200、`hits` 为空、`answer` 为固定文案，且对话网关没有被调用

#### Scenario: Citations stay inside the caller's class
- **WHEN** 任意用户发起提问
- **THEN** 返回的每个引用都属于该用户自己的班级，跨班切片既不出现在回答里也不出现在引用里

#### Scenario: Invalid question input
- **WHEN** 请求体缺失 `question`、为空串、或全为空白字符
- **THEN** 返回 400 `invalid_request`，不检索、不调用模型

#### Scenario: Model gateway failure is not disguised
- **WHEN** 检索有命中但对话网关返回错误或超时
- **THEN** 返回 503 `service_unavailable`，不返回编造的回答，也不把模型错误文本直接透出
