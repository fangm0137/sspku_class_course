# Tasks

本变更已实现并逐项 verify：以下任务全部勾选，每条 verify 都记录真实命令与输出要点。第3课的认证、班级隔离与上传入库沿用基线不动；本课在其之上追加检索能力。后端在 `backend` 执行，前端在 `frontend` 执行。

顺序为数据模型与集合 → 切分与入库 → 三种检索与班级隔离 → 问答与引用 → Compose 与前端 → 端到端验收。故障注入在应用边界进行（把 `EMBEDDING_BASE_URL`/`CHAT_BASE_URL` 指向不可达地址、停止 Qdrant、用 `docker compose up --no-deps` 单独重建 api），不用删除真实数据模拟。

## 1. 数据模型、配置与向量集合

- [x] 1.1 新增 `knowledge_chunks` 表迁移：主键作为向量主键，含 `material_id` 外键、非空且有索引的 `class_id`、`knowledge_entry_id`、`chunk_index`、`chunk_text`、`char_start`、`char_end`、`index_status`、`strategy`、`created_at`；`class_id` 使用与材料一致的复合外键。
  verify：000003 建表、000004 把字段对齐本课规约（`start_offset`/`end_offset`/`split_mode`/`embed_status` → `char_start`/`char_end`/`strategy`/`index_status`，`strategy` 加入 `hierarchy`）并补齐非空 `knowledge_entry_id`（回填后加外键）。实测 `schema_migrations` = `4/0`；第二次 `migrate up` 输出 `no change`；`SHOW CREATE TABLE` 核对到 `char_start`/`char_end`/`strategy`(`auto,custom,hierarchy`)/`index_status`(`pending,ready,failed`)、`UNIQUE uq_chunks_material_index`、`KEY idx_chunks_class_id`、`KEY idx_chunks_index_status`、`FULLTEXT ft_chunks_text WITH PARSER ngram`、`CHECK (char_end > char_start)`、复合外键 `fk_chunks_material_class` 与 `fk_chunks_knowledge_entry`。非法写入逐条被拒且未留下痕迹（探测后切片总数仍为 5）：重复 `(material_id,chunk_index)` → `1062`；`class_id` 为 NULL → `1048`；班级与材料不一致 → `1452`；`char_end <= char_start` → `3819`；`knowledge_entry_id` 不存在 → `1452`。
- [x] 1.2 增加检索相关配置项（Qdrant 地址、嵌入网关地址与密钥、对话网关地址与密钥等），必需项缺失/空值时启动失败且只报告变量名；示例配置留空、密钥不落前端与日志。
  verify：检索相关项全部按**可选**实现（缺失即关闭对应能力，进程照常启动），必需项仍是 `TOKEN_SECRET`/`DB_*`/`UPLOAD_DIR`，缺失时报错只含变量名（`cmd/server` 测试逐键覆盖）。`compose.yaml` 用 `${EMBEDDING_*:-}` 等注入且只给 `api`；`.env.example` 全部留空并注明用途。`git ls-files` 无 `.env`，`git grep` 无 `sk-`/`github_pat_` 等待推送凭据；`check-dist` 输出 `PASS build contains no server credential names or supplied values`，线上 bundle `index-CpHePMn2.js` 含 `campusclaw_chunks`/`EMBEDDING_API_KEY`/`sk-cc-`/`CHAT_API_KEY`/`qdrant:6333` 的次数均为 0；api 日志无密钥。
- [x] 1.3 启动时按配置维度确保 Qdrant 集合 `campusclaw_chunks`（余弦）存在，缺失则创建且重启幂等。
  verify：`cmd/server` 启动调用 `EnsureCollection`；未配置嵌入网关时是空操作，故启动不依赖外部服务（`TestServerStartsWithUnreachableDatabase` 仍通过）。实测集合为 `size=2048`、`distance=Cosine`、`points_count` 随切片数一致。Qdrant 停机时 `docker compose up -d --no-deps --force-recreate api`：api 健康状态 `starting`（随后 healthy），日志仅一行 `qdrant collection is not ready; vector retrieval reports dependency failure until it is`，进程不退出，`GET /health` = 200。

## 2. 切分与向量入库

- [x] 2.1 实现切分模块：`auto`（最大 800、重叠 80，空行/换行/句号断开）、`custom`（100–2000、重叠 0%–50%，可选移除 URL/邮箱或折叠空白）、`hierarchy`（`#`/`##`/`###` 分章）；预处理不改写 `body_text`，偏移相对预处理后文本。
  verify：`internal/rag` 单测覆盖三种策略的断点、重叠与偏移（`TestSplitAutoUsesWindowAndOverlap` 断言相邻切片重叠在 80 附近、`TestSplitHierarchyKeepsHeadingsInTheirSection` 断言标题留在章内且偏移可回读、`TestSplitHierarchySplitsOversizedSection`、`TestSplitPreprocessLeavesSourceUntouched`、`TestSplitRejectsUnusableInput` 覆盖非法 `custom` 与非法策略名）。接口侧 `{"strategy":"custom","size":99}` → 400 `invalid_request`，且未新增/删除切片。实测 `hierarchy` 把 905 字节正文切成 `0:0-28、1:28-432、2:432-622、3:622-905`，全部 `strategy=hierarchy`；`auto` 为 `0:0-622、1:544-905`（重叠 78 字节，向前对齐到字符边界）。重建前后 `knowledge_entries` 正文 `MD5+长度` 均为 `ad3d8e9b8eb6e8980ef03c159fba127e+323`，未被改写。
- [x] 2.2 材料入库后建立切片索引：写 `knowledge_chunks`、逐片调用嵌入网关、写 Qdrant，向量主键等于切片主键，payload 仅含标识。
  verify：上传在既有事务内写入 `materials`+`knowledge_entries`+`knowledge_chunks`（`rag.InsertChunks`），提交后再嵌入并 upsert。`reindex` 回填后材料/切片/Qdrant 三者一致：切片 5 条、`points_count=5`。payload 只含 `class_id`/`material_id`/`knowledge_entry_id`/`chunk_id`/`chunk_index`，不含正文（代码与 `SEARCH.md` 对照）。关键字检索立即可用（`CLASS_A_TXT_ONLY` 命中材料 2）。`body_text`、`materials` 行（数量/上传者/大小/名称）在重建前后逐字相同。
- [x] 2.3 建索引失败与重建：嵌入或向量库写入失败时材料保留、切片标记失败且不写不完整向量；重建索引先删该材料旧切片与旧向量再按本次策略写入，失败不自动重试。
  verify：把 `EMBEDDING_BASE_URL` 指向 `http://127.0.0.1:9` 后执行 `entrypoint /app/reindex`：材料 2 与正文仍在（各 1 行），切片状态变为 `failed`，Qdrant 点数由 5 降为 4（旧向量已删、新向量未写），该材料独有词 `CLASS_A_TXT_ONLY` 的命中数由 2 降到 1（failed 切片不再被检索），其余材料的关键字检索不受影响；恢复正常配置重跑后状态回到 `ready`、点数回到 5、命中数回到 2，命令失败时打印 `rebuild failed` 并非零退出（不自动重试）。重建的替换性：材料 5 由 `hierarchy`(4 片) → `auto`(2 片)，非当前策略残留 0 条。

## 3. 三种检索模式与班级隔离

- [x] 3.1 实现 `keyword` 模式：仅 MySQL ngram 全文索引，条件含会话班级与 `index_status='ready'`，按全文相关度排序。
  verify：`GET /api/search?mode=keyword` 返回 200、`hits=1`、`score=0.634333`（浏览器实测）/`1.108494`（curl 实测，全文相关度）。停止 Qdrant 后 keyword 仍 200 命中——证明该路径不访问向量库；该路径的 SQL 只出现在 `rag.keyword()` 一个函数内，不包含任何 HTTP 调用（不触及嵌入网关）。空查询 → 400 `{"error":"invalid_request","message":"查询内容不能为空"}`；非法模式 `mode=semantic` → 400 `{"error":"invalid_request","message":"检索模式无效"}`。
- [x] 3.2 实现 `vector` 模式：问句嵌入后在 Qdrant 按 `class_id` 过滤，余弦低于 0.35 丢弃，按向量主键回表取正文。
  verify：同义提问命中同一材料，`score=0.427672`（浏览器）/`0.429273`（curl），字段与文档一致；`excerpt` 与 MySQL `chunk_text` 逐字相同（回表取正文，见 6.3 证据映射）。阈值在 `rag.vector()` 内过滤，实测出现过余弦低于 0.35 时该词向量路径 `hits=0` 而关键字路径仍有命中的情形（阈值生效）。回表 SQL 带 `c.class_id = ? AND c.index_status='ready'`。
- [x] 3.3 实现 `hybrid` 模式与默认值：两路独立过滤后按名次执行 RRF（`k = 60`），缺席一路不贡献分数；未指定模式时按 `hybrid`。
  verify：`mode=hybrid` 的 `score=0.032787` = `2/61`，即两条路径都把它排在首位（`1/(60+1) + 1/(60+1)`）；不传 `mode` 时响应 `"mode":"hybrid"`。`FuseScored` 单测断言两路均命中的切片排在前面、单路命中仍纳入结果、名次分等于 `1/(k+rank+1)` 之和。
- [x] 3.4 实现检索侧班级隔离：请求中的 `class_id` 解析后丢弃，两条路径均使用会话班级并在回表时再次核对。
  verify：`student_b1`（B 班）检索只存在于 A 班材料的词，并在查询串写入 `class_id=1`：`mode=hybrid` 与 `mode=keyword` 均为 200 且 `hits=0`（服务端从不读取该参数）；A 班账号同问返回命中。回表 SQL 再次带 `class_id` 条件。跨班材料详情/原文沿用同形 404（`/api/materials/1` 与 `/api/materials/1/file` 对 B 班账号均 404，不存在的 id 同样 404）。
- [x] 3.5 依赖降级：Qdrant 停止时 `keyword` 仍返回结果，`vector` 与 `hybrid` 返回 503 且不编造相似度。
  verify：停 Qdrant 后 `mode=keyword` → 200 `hits=1`；`mode=vector` → 503 `{"error":"service_unavailable","message":"服务暂不可用"}`；`mode=hybrid` → 503；响应体不含任何 `score`。恢复后 vector/hybrid 重新可用（200）。未配置 `EMBEDDING_*` 时 vector/hybrid 同样 503（严格语义，见 design 实现补充第 3 条）。
- [x] 3.6 补齐检索接口说明与响应字段核对：`GET /api/search` 的字段与错误格式与 design 接口表一致。
  verify：实测响应为 `{"mode":"<mode>","hits":[{"material_id","title","chunk_id","chunk_index","char_start","char_end","excerpt","score"}]}`，逐字段与 design 接口表一致；错误统一为 `{"error":"<code>","message":"<safe text>"}`（400 `invalid_request`、401 `unauthenticated`、503 `service_unavailable`）。说明写在 `backend/SEARCH.md`，字段名与实现一致，不含内部路径或凭据。

## 4. 问答与引用

- [x] 4.1 实现 `POST /api/ask`：以混合模式取本班前 4 条切片，有命中时组装材料标题、切片序号、切片正文与提问交给对话网关，回答中 `[1]`、`[2]` 与 `citations` 顺序一致。
  verify：A 班提问「磁铁能吸木块吗？」→ 200，`answer="磁铁不能吸引木块。[1]"`，`hits` 1 条（`material_id`/`title`/`chunk_id`/`chunk_index`/`char_start`/`char_end`/`excerpt`/`citation`）与同序的 `citations`（`material_id`/`title`/`chunk_id`/`chunk_index`）。取前 4 条由 `rag.TopK` 固定，`system` 提示由服务端写入。
- [x] 4.2 无依据与输入约束：混合检索无候选时直接返回「资料中未找到相关内容」且 `citations` 为空，不调用对话网关；丢弃客户端 `system` 消息；仅以最新一句检索；不提供流式输出。
  verify：材料外问题「珠穆朗玛峰有多高？」→ 200 `{"answer":"资料中未找到相关内容。","hits":[],"citations":[]}`，且此路径在 `rag.Ask()` 命中为空时提前返回，不进入网关调用；把 `CHAT_BASE_URL` 指向不可达地址时该问题仍 200 固定文案（对照：材料内问题变 503），证明无依据时确实没有调用对话网关。携带 `{"system":"…","messages":[…]}` 的请求与普通请求返回完全相同的结果（解析器只取 `question`，其余字段被忽略）。响应为完整 JSON，无流式分片。空/超长/非 JSON 请求体 → 400。
- [x] 4.3 问答接口依赖故障与说明：嵌入或对话网关不可用或超时返回 503，不返回编造回答；更新接口说明。
  verify：`CHAT_BASE_URL=http://127.0.0.1:9` 时材料内问题 → 503 `service_unavailable`；停 Qdrant 时材料外问题（两路都无候选）→ 503，而材料内问题（关键字已有候选）→ 200 继续作答。密钥不出现在响应与日志中（`SEARCH.md` 只列字段名）。降级分工写进 `SEARCH.md` 与 design 实现补充。

## 5. Compose 与前端

- [x] 5.1 Compose 增加 `qdrant` 服务与独立命名卷 `qdrant_data`、就绪检查，仅内部网络可达、不映射宿主机端口；`api` 增加 Qdrant 与两类网关的环境变量。
  verify：`docker compose config --quiet` 通过；`docker compose ps` 显示仅 `campusclaw-nginx-1` 绑定 `127.0.0.1:8080->80`，mysql（3306/33060）与 qdrant（6333/6334）均为容器内端口未发布；`volumes` 含 `mysql_data`/`material_data`/`qdrant_data`；`api.environment` 含 `QDRANT_URL`/`QDRANT_COLLECTION`/`EMBEDDING_*`/`CHAT_*`；api 使用普通数据库账号 `campusclaw`。qdrant 就绪检查用 `bash -c 'exec 3<>/dev/tcp/127.0.0.1/6333'`（该镜像无 curl/wget，实测可行）。四个容器状态均为 healthy。
- [x] 5.2 前端检索页：一句自然语言输入、模式选择（默认 hybrid）、命中列表与摘录、跳转对应材料、问答区展示回答与 `citations`、无依据提示。
  verify：`tsc --noEmit` 与 `vite build` 通过、`check-dist` PASS。真实浏览器（Edge/headless）实跑：登录 `student_a1` → 打开「知识检索」→ 三种模式各得到 `hits=1` 并渲染出「材料标题 · 切片号 · 字符区间 · 排序分」与摘录，点击「打开对应材料」回到材料预览；页面运行时错误 0；请求记录为 `401 /api/me`、`200 /api/login`、`200 /api/materials`、三条 `200 /api/search?q=…&mode=keyword|vector|hybrid`，全部带 `Authorization: Bearer`。截图见 `frontend/test-results/search-live.png`。问答区沿用既有视图并展示 `hits`/`citations`，无依据时显示固定文案。构建产物不含网关密钥（见 1.2）。
- [x] 5.3 更新 README 与部署说明：新接口、检索模式、Qdrant 服务与卷、重建索引命令、回滚口径、非目标。
  verify：新增 `backend/SEARCH.md`（数据布局、三种切分、两个新接口与字段、降级表、backfill 命令）；`backend/README.md` 更新能力清单与迁移版本（000001–000004）；`deploy/README.md` 改为四服务、列出检索环境变量、`/app/reindex` 回填命令与回滚口径；`frontend/README.md` 增加检索页与重建控件；`.env.example` 的可选项与固定策略说明同步。文档与实现的路由、字段、返回码一致，无真实凭据。

## 6. 端到端验收

- [x] 6.1 结构校验与主路径：`openspec validate add-traceable-vector-retrieval --strict` 通过；经 Nginx 以三账号执行 `keyword`/`vector`/`hybrid` 主路径并保存不含凭据的证据。
  verify：`openspec validate … --strict` → `Change 'add-traceable-vector-retrieval' is valid`（6 个 delta：5 ADDED + 1 MODIFIED）。经 `http://127.0.0.1:8080`（Nginx）实测：`teacher_a`(A) 三模式均 `hits=1`；`student_a1`(A) `keyword=1/vector=0/hybrid=1`（该问句的最相似切片余弦低于 0.35，阈值生效）；`student_b1`(B) 三模式均 `hits=0`。证据中不含口令与 token（脚本只打印长度与字段名）。
- [x] 6.2 负向与边界：跨班检索 200 空命中、伪造 `class_id`、空查询 400、Qdrant 停机降级、无依据 `/api/ask`、嵌入失败标记、重建索引。
  verify：逐项已执行，状态码与响应见 3.1/3.4/3.5/2.3/4.2 的实测记录：跨班+伪造 `class_id` → 200 `hits=0`；空查询 → 400；非法模式 → 400；非法 `custom` 参数 → 400 且不动切片；学生重建索引 → 403；跨班重建 → 404；Qdrant 停机 → keyword 200 / vector·hybrid 503；无依据问答 → 200 固定文案；对话网关故障 → 503；嵌入失败 → 切片 `failed`、材料保留、点数下降、恢复后回到 `ready`。
- [x] 6.3 场景证据映射与范围核对：为每个 Scenario 建立证据映射；核对未引入流式输出、重排序、编排框架、跨班检索、自动重新切分等非目标，且 `class-material-access` 基线需求未被破坏。
  verify：`knowledge-retrieval` 的 5 条需求 25 个 Scenario 映射如下——「Index built after successful upload」→2.2 实测；「Three chunking strategies」→2.1 实测（auto 0-622/544-905、hierarchy 4 片、`custom(600/60)` 重建返回 200）；「Preprocessing leaves source untouched」→2.1 正文 MD5 前后一致 + 单测；「Embedding failure keeps material record」→2.3 注入实测；「Rebuild index replaces stale chunks」→2.3 替换实测；「Invalid chunking parameters」→2.1 400 实测；「Keyword search does not embed」→3.1（停 Qdrant 仍命中）；「Vector search tolerates wording changes」→3.2（score 0.4277）；「Hybrid fuses two rankings」→3.3（score `2/61`）；「Default mode and empty query」→3.3/3.1；「Vector dependency unavailable」→3.5；「Answered with citations」→4.1；「No evidence means no generation」→4.2（含对话网关故障对照）；「Client system message is discarded」→4.2；「Non-streaming response」→4.2；「Generation dependency unavailable」→4.3；「Session class overrides request class」「Cross class retrieval appears as no hits」「Both paths carry the class condition」「No padding when nothing is relevant」→3.4/3.2；「Material detail keeps 404 semantics」→3.4；「Split storage layout」「Primary key consistency」→2.2 + `SHOW CREATE TABLE`；「Model gateways and vector store not client reachable」→5.1（端口未发布）+1.2（bundle 无密钥）；「Keyword path avoids both gateways」→3.1。范围核对：未引入流式输出（响应为完整 JSON）、未引入重排序或分数归一化加权、未引入 LangChain/LlamaIndex 等编排框架（`go.mod` 未新增依赖，`internal/rag` 全标准库）、跨班检索对外仍是 200 空命中、未重建的材料不会自动按新策略切分；`class-material-access` 的 9 条基线需求未被修改（`openspec/specs/class-material-access/spec.md` 未出现在本变更 diff 中）。
