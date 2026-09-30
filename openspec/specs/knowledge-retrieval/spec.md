# knowledge-retrieval Specification

## Purpose
为 CampusClaw 提供限定班级范围的知识库检索能力：把已入库正文切分为切片并向量化，支持关键字、向量与混合三种检索，命中可回溯到原文并标注出处；当资料中不存在依据时明确返回未找到，不得编造出处。

## Requirements

### Requirement: Chunked search index built on ingestion

系统 SHALL 在材料入库成功后，按切分策略将知识库正文 `body_text` 切分为若干切片写入 `knowledge_chunks`，并将每个切片嵌入为一条向量写入向量库；向量主键 MUST 等于该切片的 `knowledge_chunks.id`。切分策略 MUST 支持 `auto`（最大约 800 字、重叠 80 字，优先在空行、换行、句号处断开）、`custom`（最大 100–2000 字、重叠 0%–50%）与 `hierarchy`（按 Markdown `#`/`##`/`###` 标题分章，标题保留在章节切片内）；未指定策略时 MUST 按 `auto` 处理，种子材料补齐索引同样采用 `auto`。预处理（移除 URL/邮箱、折叠连续空白）SHALL 仅作用于待切分与待嵌入文本，`knowledge_entries.body_text` 保持上传原文，切片偏移量相对预处理后文本计算。嵌入或写入向量库失败时，材料与知识库记录 MUST 仍然保留，对应切片标记为失败状态，MUST NOT 写入不完整的向量数据。改变策略 MUST 通过显式重建索引生效：先删除该材料的旧切片与旧向量，再按本次请求的策略重新写入。

#### Scenario: Index built after successful upload
- **WHEN** 教师成功上传一份合法 txt/md 材料
- **THEN** 系统为其生成若干 `knowledge_chunks` 切片与对应的向量库记录，向量主键与切片 `knowledge_chunks.id` 相同；材料与 `knowledge_entries` 记录保持原有语义不变

#### Scenario: Three chunking strategies
- **WHEN** 对同一份含 Markdown 标题的正文分别以 `auto`、`custom`、`hierarchy` 构建索引
- **THEN** `auto` 按最大约 800 字、重叠 80 字在空行/换行/句号处断开；`custom` 按请求给定的最大长度（100–2000）与重叠比例（0%–50%）切分；`hierarchy` 按标题层级分章且标题保留在章内；未指定策略时结果等同于 `auto`

#### Scenario: Preprocessing leaves source untouched
- **WHEN** 使用移除 URL/邮箱或折叠空白的 `custom` 参数完成切分
- **THEN** `knowledge_entries.body_text` 仍为上传原文，记录中的切片偏移量相对预处理后的文本计算

#### Scenario: Embedding failure keeps material record
- **WHEN** 切分成功后嵌入调用或向量库写入失败
- **THEN** 材料与知识库记录仍然存在，对应切片标记为失败状态且不写入向量数据；关键字检索仍可基于 MySQL 正文工作

#### Scenario: Rebuild index replaces stale chunks
- **WHEN** 教师以新的策略重建某材料索引
- **THEN** 该材料的旧切片与旧向量被删除后按新策略重新写入；未重建时旧切片与旧向量保留，因此不会自动按新策略重新切分

#### Scenario: Invalid chunking parameters
- **WHEN** 请求指定的 `custom` 最大长度不在 100–2000、重叠比例不在 0%–50%，或策略名非法
- **THEN** 返回 400，且不新增、不删除任何切片或向量

### Requirement: Class scoped keyword, vector and hybrid retrieval

系统 SHALL 提供班级范围内的检索接口，支持 `keyword`、`vector`、`hybrid` 三种模式，未指定时 MUST 采用 `hybrid`。`keyword` 模式 MUST 仅查询 MySQL 全文索引（ngram，token 长度 2），不调用嵌入服务、不访问向量库，按全文相关度由高到低返回；`vector` 模式 MUST 先将问句嵌入，在向量库按班级过滤后计算余弦相似度，丢弃低于 0.35 的候选，再按向量主键回关系库取回正文，按相似度由高到低返回；`hybrid` 模式 MUST 同时执行上述两条路径，各自独立过滤后按名次执行 RRF（`k = 60`）融合，缺席的一路不贡献分数。三种模式最终 MUST 都回到关系库取回正文，MUST NOT 在向量库中保存正文或直接以向量库内容作为正文来源。空查询 MUST 返回 400。向量库不可用时，`keyword` MUST 仍能返回结果，`vector` 与 `hybrid` MUST 返回 503 且 MUST NOT 编造相似度分数。

#### Scenario: Keyword search does not embed
- **WHEN** 问句中的词直接出现在本班原文，以 `keyword` 模式检索
- **THEN** 命中的切片按全文相关度排序返回，未调用嵌入服务，也未访问向量库

#### Scenario: Vector search tolerates wording changes
- **WHEN** 用与原文语义相近但措辞不同的问句，以 `vector` 模式检索
- **THEN** 问句先被转换为向量，在向量库按班级过滤后按余弦相似度排序，余弦相似度低于 0.35 的候选被丢弃，命中切片以向量主键回关系库取回正文

#### Scenario: Hybrid fuses two rankings
- **WHEN** 以 `hybrid` 模式检索，其中一部分切片在两条路径上都命中
- **THEN** 两路先各自按绝对分数过滤，再按名次执行 RRF（`k = 60`）融合；两路均命中的切片排序更靠前，某一缺席路径不贡献分数

#### Scenario: Default mode and empty query
- **WHEN** 请求未指定检索模式，或询问内容为空
- **THEN** 未指定模式时按 `hybrid` 执行；空查询返回 400

#### Scenario: Vector dependency unavailable
- **WHEN** 向量库停止服务时分别以三种模式检索
- **THEN** `keyword` 仍返回正文结果；`vector` 与 `hybrid` 返回 503，且不返回推算或编造的相似度分数

### Requirement: Traceable hits and grounded answers

每条检索命中 MUST 可回溯至原文，至少包含材料标题、切片序号、字符区间与一段摘录，并 MUST 支持打开对应材料；摘录 MUST 取自关系库中的切片正文，而不是向量库。系统 SHALL 提供问答接口 `POST /api/ask`：以混合模式取回本班前 4 条切片；存在命中时，将材料标题、切片序号、切片正文与用户本轮提问交给对话网关生成简短回答，回答中的 `[1]`、`[2]` 标注 MUST 与 `citations` 顺序一致。混合检索无候选时，接口 MUST 直接返回「资料中未找到相关内容」且 `citations` 为空，MUST NOT 调用对话网关。系统提示 MUST 由服务端写入，客户端注入的 `system` 消息 MUST 被丢弃；检索 MUST 仅以最新一句提问发起。本课 MUST NOT 提供流式输出。嵌入或对话网关不可用且该请求需要它们时 MUST 返回 503，MUST NOT 返回编造的回答。

#### Scenario: Answered with citations
- **WHEN** 本班材料中确有依据并调用 `POST /api/ask`
- **THEN** 系统以混合模式取回本班前 4 条切片，将标题、序号、正文与提问交给对话网关；回答中以 `[1]`、`[2]` 指回出处，`citations` 顺序与标注一致

#### Scenario: No evidence means no generation
- **WHEN** 提问内容与本班材料无关（例如天气、比分），混合检索无候选切片
- **THEN** 接口直接返回「资料中未找到相关内容」且 `citations` 为空，对话网关不被调用，不使用模型记忆作答

#### Scenario: Client system message is discarded
- **WHEN** 请求中附带客户端构造的 `system` 消息或历史轮次
- **THEN** 该 `system` 消息被丢弃，系统提示由服务端写入；检索仅以最新一句提问发起

#### Scenario: Non-streaming response
- **WHEN** 调用 `POST /api/ask`
- **THEN** 返回完整 JSON，不返回流式分片；本课不提供流式输出

#### Scenario: Generation dependency unavailable
- **WHEN** 已完成检索但嵌入或对话网关不可用
- **THEN** 返回 503，不返回编造的回答

### Requirement: Retrieval class isolation

检索侧的班级标识 MUST 仅取自登录会话；查询字符串、请求头或 JSON 正文中携带的班级编号在解析后 MUST 被丢弃，不得改变过滤范围。`keyword` 与 `vector` 两条路径 MUST 都使用会话班级过滤（向量路径在向量库过滤、关键字路径在关系库过滤且仅检索索引就绪的切片），并在回关系库取回正文时以同一班级条件再次核对；payload 中的编号 MUST NOT 单独作为正文来源。跨班级检索对外 MUST 表现为 HTTP 200 与空命中（或「资料中未找到相关内容」），MUST NOT 以 403 或 404 暗示资料归属；按材料标识打开详情沿用既有规则，跨班级与不存在均返回 404。

#### Scenario: Session class overrides request class
- **WHEN** 以 A 班身份检索，问句仅出现在 B 班正文中，并在请求体或查询串中写入 B 班的班级编号
- **THEN** 实际过滤条件仍为会话所属的 A 班，返回 200 与空命中；请求中携带的班级编号被丢弃

#### Scenario: Cross class retrieval appears as no hits
- **WHEN** 以 A 班身份检索一个只出现在 B 班正文中的词
- **THEN** 响应为 HTTP 200 且命中为空（或「资料中未找到相关内容」），不使用 403 或 404 向调用方暗示该资料属于其他班级

#### Scenario: Both paths carry the class condition
- **WHEN** 分别检查 `keyword` 与 `vector` 两条检索路径
- **THEN** 两条路径都按会话班级过滤，回表取正文时再次核对班级；即使单侧漏加过滤，另一侧仍排除其他班级的记录

#### Scenario: Material detail keeps 404 semantics
- **WHEN** 以本班会话打开属于其他班级的材料详情或原文
- **THEN** 沿用既有规则返回同形 404，不泄露材料归属

#### Scenario: No padding when nothing is relevant
- **WHEN** 提问内容与本班材料无关
- **THEN** 命中列表为空，不以相关度明显偏低的切片凑数

### Requirement: Split vector storage and server only model gateways

系统 SHALL 将切片正文与字符偏移保存在关系库的 `knowledge_chunks` 及关联记录中，将定长浮点向量保存在向量库中；向量库保存的 payload MUST 仅包含标识类字段（`class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`），MUST NOT 保存正文副本。同一存在对应关系的向量主键、`knowledge_chunks.id` 与 `payload.chunk_id` MUST 三者相同。嵌入网关与对话网关 MUST 仅由服务端调用，MUST NOT 暴露给浏览器，相关密钥 MUST NOT 下发到前端；向量库 MUST NOT 对客户端开放访问（不将端口映射到宿主机）。系统 MUST NOT 引入 LangChain/LlamaIndex 等编排框架。嵌入网关将文本转换为向量、对话网关依据切片生成回答，二者 MUST 为相互独立的调用；关键字检索 MUST NOT 触及二者。

#### Scenario: Split storage layout
- **WHEN** 对照同一条切片在关系库与向量库中的存储
- **THEN** 关系库保存 `body_text`、`chunk_text` 与字符偏移；向量库保存定长浮点向量，payload 仅含 `class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`，不含正文字符串

#### Scenario: Primary key consistency
- **WHEN** 检查同一条切片在该材料索引中的记录
- **THEN** 向量主键、`knowledge_chunks.id` 与 `payload.chunk_id` 三者相同，可直接按主键回关系库取回正文

#### Scenario: Model gateways and vector store not client reachable
- **WHEN** 客户端尝试直接访问向量库端口或两类模型网关
- **THEN** 均不可达；客户端仅能调用本站检索与问答接口，密钥不出现于浏览器

#### Scenario: Keyword path avoids both gateways
- **WHEN** 以 `keyword` 模式检索
- **THEN** 既不调用嵌入网关也不调用对话网关，且不访问向量库
