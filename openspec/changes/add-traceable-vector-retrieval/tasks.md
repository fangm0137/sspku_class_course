# Tasks

本变更处于规约阶段：以下任务均保持未勾选，逐项实现并执行 verify 后才勾选，不把文档生成当作功能实现。第3课的认证、会话、班级隔离与上传入库已归档为基线，本课在其之上追加检索能力，不改动既有行为合同。后端预定目录 `backend`，前端 `frontend`；后端命令在 `backend` 执行，前端在 `frontend` 执行。每条实现任务同时交付对应测试与必要接口说明，记录真实命令和输出。

顺序为数据模型与集合 → 切分与入库 → 三种检索与班级隔离 → 问答与引用 → Compose 与前端 → 端到端验收。测试使用独立 MySQL、Qdrant 与一次性凭据，不污染既有种子；故障注入在应用边界进行，不用删除真实数据模拟。

## 1. 数据模型、配置与向量集合

- [ ] 1.1 新增 `knowledge_chunks` 表迁移：主键作为向量主键，含 `material_id` 外键、非空且有索引的 `class_id`、`knowledge_entry_id`、`chunk_index`、`chunk_text`、`char_start`、`char_end`、`index_status`、`strategy`、`created_at`；`class_id` 使用与材料一致的复合外键。verify：真实 MySQL 8.0 上两次 up、`SHOW CREATE TABLE` 核对非空列/索引/唯一/外键，重复插入与 NULL `class_id`、班级不一致被拒，并记录命令输出。
- [ ] 1.2 增加检索相关配置项（Qdrant 地址、嵌入网关地址与密钥、对话网关地址与密钥等），必需项缺失/空值时启动失败且只报告变量名；示例配置留空、密钥不落前端与日志。verify：缺失必需项时启动失败并展示错误，随后恢复；比对 `.env.example` 与代码读取项；确认 git 跟踪文件与构建产物无真实凭据。
- [ ] 1.3 启动时按配置维度确保 Qdrant 集合 `campusclaw_chunks`（余弦）存在，缺失则创建且重启幂等。verify：真实 Qdrant 上启动后核对集合度量方式与向量维度；Qdrant 不可用时进程不退出，`GET /health` 仍为 200 而检索接口返回 503。

## 2. 切分与向量入库

- [ ] 2.1 实现切分模块：`auto`（最大 800、重叠 80，空行/换行/句号断开）、`custom`（100–2000、重叠 0%–50%，可选移除 URL/邮箱或折叠空白）、`hierarchy`（`#`/`##`/`###` 分章）；预处理不改写 `body_text`，偏移相对预处理后文本。verify：单元测试三种策略的断点、重叠与偏移；非法 `custom` 参数返回 400 且不改动切片；`body_text` 保持不变。
- [ ] 2.2 材料入库后建立切片索引：写 `knowledge_chunks`、逐片调用嵌入网关、写 Qdrant，向量主键等于切片主键，payload 仅含标识。verify：真实 MySQL + Qdrant 上传后切片数与向量数一致、主键相同、payload 无正文；`materials` 与 `knowledge_entries` 语义不变；关键字检索已可工作。
- [ ] 2.3 建索引失败与重建：嵌入或向量库写入失败时材料保留、切片标记失败且不写不完整向量；重建索引先删该材料旧切片与旧向量再按本次策略写入，失败不自动重试。verify：故障注入后材料仍在、失败切片状态正确、无残留向量；重建后旧切片不存在且新切片符合新策略；未重建时旧切片保留。

## 3. 三种检索模式与班级隔离

- [ ] 3.1 实现 `keyword` 模式：仅 MySQL ngram 全文索引，条件含会话班级与 `index_status='ready'`，按全文相关度排序。verify：词直接出现在本班原文时命中且排序合理；断言未调用嵌入网关、未访问 Qdrant；空查询返回 400，非法模式返回 400。
- [ ] 3.2 实现 `vector` 模式：问句嵌入后在 Qdrant 按 `class_id` 过滤，余弦低于 0.35 丢弃，按向量主键回表取正文。verify：语义改写问句可命中；低于阈值候选被丢弃；`excerpt` 等于 MySQL `chunk_text`；回表带班级与 `ready` 条件。
- [ ] 3.3 实现 `hybrid` 模式与默认值：两路独立过滤后按名次执行 RRF（`k = 60`），缺席一路不贡献分数；未指定模式时按 `hybrid`。verify：两路均命中的切片排序靠前；仅单路命中的切片纳入结果；缺省模式断言为 hybrid。
- [ ] 3.4 实现检索侧班级隔离：请求中的 `class_id` 解析后丢弃，两条路径均使用会话班级并在回表时再次核对。verify：A 班会话检索仅存在于 B 班正文中的词，且请求体/查询串写入 B 班编号，仍返回 200 空命中；他班材料详情沿用 404；可举证两路过滤均取自会话班级。
- [ ] 3.5 依赖降级：Qdrant 停止时 `keyword` 仍返回结果，`vector` 与 `hybrid` 返回 503 且不编造相似度。verify：停止 Qdrant 后分别请求三种模式并记录状态码与响应体，恢复后 `vector`/`hybrid` 重新可用。
- [ ] 3.6 补齐检索接口说明与响应字段核对：`GET /api/search` 的字段与错误格式与 design 接口表一致。verify：逐字段比对 `mode`/`hits`（含 `material_id`/`title`/`chunk_id`/`chunk_index`/`char_start`/`char_end`/`excerpt`/`score`）与统一 `error`/`message`；文档不含内部路径或凭据。

## 4. 问答与引用

- [ ] 4.1 实现 `POST /api/ask`：以混合模式取本班前 4 条切片，有命中时组装材料标题、切片序号、切片正文与提问交给对话网关，回答中 `[1]`、`[2]` 与 `citations` 顺序一致。verify：有依据时回答含引用且顺序一致；`answer`/`citations` 字段与 design 一致。
- [ ] 4.2 无依据与输入约束：混合检索无候选时直接返回「资料中未找到相关内容」且 `citations` 为空，不调用对话网关；丢弃客户端 `system` 消息；仅以最新一句检索；不提供流式输出。verify：无关提问返回固定文案且断言对话网关未被调用；携带 `system` 与历史轮次不改变行为；响应为完整 JSON。
- [ ] 4.3 问答接口依赖故障与说明：嵌入或对话网关不可用或超时返回 503，不返回编造回答；更新接口说明。verify：注入网关故障后 `/api/ask` 为 503；密钥不出现在响应与日志；文档与实现一致。

## 5. Compose 与前端

- [ ] 5.1 Compose 增加 `qdrant` 服务与独立命名卷 `qdrant_data`、就绪检查，仅内部网络可达、不映射宿主机端口；`api` 增加 Qdrant 与两类网关的环境变量。verify：`docker compose config` 核对端口/网络/卷/env；仅 Nginx 暴露 localhost 端口；Qdrant 无宿主机端口；api 使用普通数据库账号。
- [ ] 5.2 前端检索页：一句自然语言输入、模式选择（默认 hybrid）、命中列表与摘录、跳转对应材料、问答区展示回答与 `citations`、无依据提示。verify：`npm run build` 通过；浏览器验证三种模式与引用标注；构建产物与页面无网关密钥；401/503 分别按既有语义处理。
- [ ] 5.3 更新 README 与部署说明：新接口、检索模式、Qdrant 服务与卷、重建索引命令、回滚口径、非目标。verify：从零按说明可启动并检索；文档与实际路由、字段、返回码一致；无真实凭据。

## 6. 端到端验收

- [ ] 6.1 结构校验与主路径：`openspec validate add-traceable-vector-retrieval --strict` 通过；经 Nginx 以三账号执行 `keyword`/`vector`/`hybrid` 主路径并保存不含凭据的证据。
- [ ] 6.2 负向与边界：跨班检索 200 空命中、伪造 `class_id`、空查询 400、Qdrant 停机降级、无依据 `/api/ask`、嵌入失败标记、重建索引。verify：逐项执行并记录真实状态码与响应，未执行项明示未执行。
- [ ] 6.3 场景证据映射与范围核对：为每个 Scenario 建立证据映射；核对未引入流式输出、重排序、编排框架、跨班检索、自动重新切分等非目标，且 `class-material-access` 基线需求未被破坏。
