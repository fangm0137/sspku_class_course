# Design

## Context

动机见 proposal.md。第3课的成果已归档为基线 `openspec/specs/class-material-access/spec.md`（9 条需求）：Go 后端 + MySQL 8.0 + React 18 TS Vite + Nginx，服务端会话、可信班级、材料上传入库（`materials` + `knowledge_entries` 同事务）与 Compose 三服务均已验收，本课不改动这些行为合同。

现状约束：`openspec/changes/archive/2026-09-28-add-auth-rbac-class-knowledge` 提供了可复用的认证中间件、请求上下文中的可信身份与班级、材料模块与迁移工具（golang-migrate 显式 SQL）。后端目录为 `backend/`，材料与正文目前只做持久化，没有切分、向量或语义检索。前端为 `frontend/`，通过 Nginx 同源访问 `/api/`。新增外部依赖只有 Qdrant 与两类模型网关。

## Goals / Non-Goals

**Goals:** 在本班范围内实现"入库即建索引、按语义检索、命中可回溯、无依据不生成"的闭环，并可通过 API 与浏览器独立验证；班级过滤在关键字与向量两条路径上都成立，请求中的班级参数无法扩大可见范围。

**Non-Goals:** 沿用 proposal 的排除项。设计层面另明确：不引入编排框架（LangChain/LlamaIndex/Dify），不实现交叉编码器重排序、分数归一化加权、按班级拆分向量集合、多副本/生产高可用、流式输出与多轮编排；不为已入库材料自动重新切分；不将向量库或网关暴露为浏览器入口。

## Decisions

### 1. 组件落位与技术选型

沿用第3课架构，在 Go 服务内新增"检索模块"，并把切分、嵌入、检索、问答收敛在同一模块内，便于逐条对照规约验收。

- 切分与正文读取落在 `backend/internal/knowledge/`：输入为 `knowledge_entries.body_text` 与本次策略，输出为切片（文本、序号、预处理文本中的字符偏移与区间）。
- 嵌入、向量写入、检索与问答落在 `backend/internal/retrieval/`：负责调用嵌入网关、读写 Qdrant、执行三种检索与 RRF、组装问答上下文。
- HTTP 层新增检索与问答路由（`GET /api/search`、`POST /api/ask`，字段见"接口响应约定"），复用既有的会话认证中间件与请求上下文中的可信 `class_id`、材料模块。

选型（依据 `week04` 规约与课件）：切分自研、嵌入走兼容 OpenAI `/embeddings` 的课程网关、向量库用 Compose 内的 Qdrant（集合 `campusclaw_chunks`，余弦距离）、关键字用 MySQL `FULLTEXT ... WITH PARSER ngram`（token 长度 2）、融合用 RRF（`k = 60`）。备选方案与取舍：Milvus/pgvector/Chroma 需要额外运维或把向量并入关系库、pgvector 会让班级过滤与事务设计重做，故不选；引入编排框架会削弱"逐条对照规约"的可验收性，故不选。分数归一化后加权求和作为备选，但更换嵌入模型需重新标定权重，故采用对名次稳健的 RRF。

### 2. 切分、嵌入与索引生命周期

策略与参数（规约已固定）：`auto` 最大 800 字、重叠 80 字，优先在空行/换行/句号断开；`custom` 最大 100–2000 字、重叠 0%–50%，无断点处按最大长度强制截断，可选移除 URL/邮箱或折叠连续空白；`hierarchy` 按 `#`/`##`/`###` 分章，标题保留在章内，单章过长时再按 `auto` 窗口切分。未指定策略等同 `auto`。

偏移基准：预处理不改写 `body_text`；当启用移除链接或折叠空白时，切片偏移相对**预处理后文本**计算，不再是原文件字符下标。切片记录同时保存预处理后文本中的区间，用于摘录与定位，并在字段注释与接口文档中明示该基准。

索引生命周期：上传事务提交后触发建索引——先按策略写入切片（`knowledge_chunks`），逐片调用嵌入网关，再写入 Qdrant（主键为切片主键）。任一步失败时，材料与 `knowledge_entries` 记录保留，失败切片标记为 `failed`，不写入向量数据，也不回滚已提交的材料事务（与 `class-material-access` 的"嵌入失败材料仍在"一致）。重建索引为显式操作：先删除该材料全部旧切片与旧向量，再按**本次请求**的策略重新写入；重建失败不自动重试。

新增表 `knowledge_chunks`（版本化 SQL 迁移，不使用 ORM 自动建表）：`id`（主键，同时作为向量主键）、`material_id` 外键、非空且带索引的 `class_id`、`knowledge_entry_id`、`chunk_index`、`chunk_text`、`char_start`、`char_end`、`index_status`（`ready`/`failed`）、`strategy`、`created_at`。`class_id` 与材料保持一致的复合外键沿用第3课做法，防止班级归属漂移。

### 3. 三种检索模式与融合

`keyword`：仅 MySQL 全文索引（ngram，token 2），`WHERE class_id = 会话班级 AND index_status = 'ready'`，按全文相关度排序；不调用嵌入网关、不访问 Qdrant。

`vector`：先对问句调用嵌入网关得到向量，在 Qdrant 中带 `class_id` 过滤查询，丢弃余弦相似度低于 **0.35** 的候选，取前若干名次，再以向量主键回 MySQL 取正文（回表仍带班级条件与 `ready` 状态）。

`hybrid`：并行执行上述两条路径，各自先按**绝对分数过滤**（向量 0.35、关键字全文相关度门槛），再仅按**名次**执行 RRF，`score = Σ 1/(k + rank)`，`k = 60`；缺席的一路不贡献分数。名次分不是相关性阈值，仅用于排序。默认模式为 `hybrid`。

### 4. 存储分离与主键一致

切片正文只存 MySQL（`knowledge_chunks.chunk_text`），摘录一律取自该字段。Qdrant 只存定长浮点向量与标识 payload：`class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`，不存正文副本。向量主键、`knowledge_chunks.id` 与 `payload.chunk_id` 三者相同，因此可直接按主键回表，无需在向量库重复保存字符串。嵌入维度必须与 `campusclaw_chunks` 集合配置一致，由初始化时按配置的模型维度创建；更换嵌入模型必须重建索引。

### 5. 检索侧班级隔离

班级只从会话派生的请求上下文读取；查询串、请求头与 JSON 正文中的 `class_id` 解析后丢弃。两条路径都带班级条件：关键字在关系库过滤、向量在 Qdrant 过滤；回表取正文时以同一班级再次核对，payload 中的编号不得单独作为正文来源。跨班级检索对外表现为 HTTP 200 与空命中（或「资料中未找到相关内容」），不使用 403/404 暴露归属；材料详情/原文仍沿用第3课的同形 404。

### 6. 问答接口与引用

`POST /api/ask` 自身不实现第三种检索：它先以 `hybrid` 取回本班前 **4** 条切片。无候选时在检索模块处直接返回「资料中未找到相关内容」且 `citations` 为空，**不调用对话网关**。有候选时，由检索模块组装对话网关输入：每条切片的材料标题、切片序号、切片正文，加上用户本轮提问；回答中的 `[1]`、`[2]` 与 `citations` 顺序一致。系统提示由服务端写入，客户端注入的 `system` 消息丢弃；检索仅以最新一句提问发起；本课不做流式输出，返回完整 JSON。对话模块不得自行访问数据库或向量库检索其他材料。

### 7. 失败与降级

- 空查询：400。
- Qdrant 不可用：`keyword` 仍可返回正文结果；`vector` 与 `hybrid` 返回 503，不编造相似度分数。
- 嵌入失败（建索引阶段）：材料保留，切片 `failed`；检索阶段嵌入失败时按依赖不可用处理（503）。
- 对话网关不可用：`/api/ask` 返回 503，不返回编造回答。
- 网关与向量库地址/密钥仅从环境注入，不写入前端构建产物、不落日志。

### 8. 配置、Compose 与前端

Compose 增加 `qdrant` 服务与独立命名卷 `qdrant_data`（集合持久化），仅内部网络可达，不映射宿主机端口；`api` 增加 Qdrant 地址与两类网关地址/密钥的环境变量（示例项写空值）；初始化顺序在 MySQL 就绪后追加 Qdrant 就绪检查。`GET /health` 语义不变（仍只表示进程存活，不查询数据库/向量库/网关）。前端新增检索页：输入一句自然语言，选择模式（默认 hybrid），展示命中列表与摘录，可跳转对应材料；问答区展示回答与 `citations`，引用按序号标注，无依据时显示「资料中未找到相关内容」。

## 接口响应约定

在既有九路由基础上新增两个入口，均要求有效登录会话，除空查询外不新增 415 类别。

| 接口 | 成功状态与 JSON | 失败状态 |
| --- | --- | --- |
| GET /api/search?q=&mode= | 200：`{"mode":"<keyword|vector|hybrid>","hits":[{"material_id":<int>,"title":"<string>","chunk_id":<int>,"chunk_index":<int>,"char_start":<int>,"char_end":<int>,"excerpt":"<string>","score":<number>}]}`；空结果 `{"mode":"<...>","hits":[]}` | 400（空查询/非法模式）、401、503（向量或嵌入依赖不可用，仅 vector/hybrid） |
| POST /api/ask | 200：`{"answer":"<string>","citations":[{"material_id":<int>,"title":"<string>","chunk_id":<int>,"chunk_index":<int>}]}` | 400、401、503 |

错误沿用统一 `{"error":"<code>","message":"<safe text>"}`；无命中时 `hits`/`citations` 为空数组，`/api/ask` 的 `answer` 为固定文案「资料中未找到相关内容」。`score` 为所选模式的排序分（向量为余弦相似度、关键字为全文相关度、混合为 RRF 名次分），仅供排序参考，不作为相关性阈值对外承诺。

## Risks / Trade-offs

- 关系库与向量库双存储可能不一致 → 主键一致 + 回表按班级与状态核对 + 建索引失败只标记 `failed`、不写不完整向量；重建为显式操作。
- Qdrant 成为新的运行依赖 → `keyword` 路径可独立降级；健康检查不依赖 Qdrant，业务接口按 503 表达；Compose 独立就绪检查。
- 嵌入模型/维度变更会使既有向量失效 → 维度与集合绑定，更换模型必须重建索引；文档明示。
- 0.35 阈值与 `k = 60` 是启发式参数 → 文档说明"名次分不是相关性阈值"，阈值仅用于候选过滤。
- 预处理后偏移与原文件下标不一致 → 字段与接口文档明示偏移基准为预处理后文本，避免误解为原文件下标。
- 网关为外部依赖且涉及密钥 → 仅服务端调用、密钥不落前端与日志、超时映射 503；单个网关故障不静默返回空结果。

## Migration Plan

1. 迁移新增 `knowledge_chunks` 表（up/down 版本化 SQL），保留第3课五张业务表不变。
2. 应用启动时按配置维度确保 Qdrant 集合 `campusclaw_chunks`（余弦）存在，缺失则创建。
3. 对第3课已入库的历史材料执行一次**显式**重建索引（backfill），使旧材料具备切片与向量；该步骤为人工触发的命令，失败不自动重试。
4. Compose 增加 `qdrant` 服务、`qdrant_data` 卷与就绪检查；确认 `keyword` 模式在无 Qdrant 时仍可用。
5. 回滚：本变更对既有能力为叠加；停用检索/问答路由与前端入口即可退回第3课行为，`knowledge_chunks` 与 `qdrant_data` 可丢弃，不影响 `materials`、`knowledge_entries` 与账号会话。

## Open Questions

- 课程嵌入网关的具体地址、密钥与模型维度：实现时从环境注入并在 `knowledge_chunks`/集合初始化中固定；维度以网关实际返回为准。
- 对话网关的模型与超时/重试口径：实现时按 design 的失败约定固定，不改动规约。

`GET /health` 与错误优先级沿用 `openspec/specs/class-material-access/spec.md`；本变更不修改该能力的需求。

## 实现补充

以下为实现阶段在规约范围内的落定细节，逐项都能在代码与实测里核对。

1. **字段命名统一**：`knowledge_chunks` 的 `start_offset`/`end_offset`/`split_mode`/`embed_status` 更名为 `char_start`/`char_end`/`strategy`/`index_status`（迁移 000004），并补齐非空 `knowledge_entry_id`（回填后加外键）。因此本变更的 delta 同时包含 `specs/knowledge-qa/spec.md` 的 MODIFIED 需求：字段名对齐，`strategy` 取值集合加入 `hierarchy`，行为不变。
2. **模块落位**：切分、融合、网关与向量库客户端合并落在 `backend/internal/rag/`（单个包，全标准库），HTTP 层为 `internal/httpapi/search.go` 与 `ask.go`。design 里原先设想的 `internal/knowledge` 与 `internal/retrieval` 两个包合并为一个，避免为了分层而在纯计算与外部调用之间传递重复结构体。
3. **依赖故障语义的分工**：`GET /api/search` 按本课规约对 `hybrid` 取严格语义——两条路径都要能执行，任一路径不可用即 503；`POST /api/ask` 按 `knowledge-qa` 基线取降级语义——向量路径在未配置 `EMBEDDING_*` 时自动停用，配置了但故障时若关键字已有候选则继续据此作答，两条路径都无候选才 503。两者共用同一检索实现，只有"路径故障如何处置"不同。
4. **`/api/ask` 同时返回 `hits` 与 `citations`**：`hits` 是可回溯摘录列表（材料标题、切片序号、字符区间、摘录），`citations` 是同一顺序的紧凑标识 `{material_id,title,chunk_id,chunk_index}`，与回答里的 `[1]`、`[2]` 一一对应。两份规约对字段名的表述不同，这里以并存方式同时满足。
5. **重建索引入口**：`POST /api/materials/{id}/reindex`（仅教师、仅本班，其他班级与不存在同为 404），参数为 `{strategy,size,overlap,remove_urls,remove_emails,collapse_whitespace}`；执行顺序是"先删该材料的旧向量与旧切片，再按本次策略写入"。历史材料的一次性补齐用 `cmd/reindex`（可带材料 id，省略即全部）。
6. **集合初始化**：`server` 启动时按配置维度确保集合存在；未配置嵌入网关时是空操作，Qdrant 不可用时只记录日志，进程照常提供关键字检索。就绪探测沿用"`/health` 只表示进程存活"的约定。
