# Tasks

> 先规约后实现：以下任务逐项实现并执行 verify 后才勾选，完成后归档并写入主规约 `openspec/specs/knowledge-qa/spec.md`。
>
> 本机没有 Go 与 PowerShell 验收环境：`go vet ./...`、`go test ./...` 在 golang 容器内执行，前端用本机 Node 执行 `tsc` 与 `vite build`，端到端在运行中的 Compose 栈上实测。

## 1. 切片与数据模型

- [x] 1.1 新增迁移 `000003_knowledge_chunks.up/down.sql`：建 `knowledge_chunks`（含 `chunk_index` 唯一、`class_id` 索引、与 materials 的复合外键）并建 `FULLTEXT KEY ft_chunks_text (chunk_text) WITH PARSER ngram`。
  verify：本机实测 `migrate up` 输出 `migrations applied`；`SHOW CREATE TABLE` 可见 `uq_chunks_material_index`、`idx_chunks_class_id`、复合外键 `fk_chunks_material_class`、`CHECK (end_offset > start_offset)` 与 `FULLTEXT KEY ft_chunks_text ... WITH PARSER ngram`；关键字检索能命中，证明 ngram 索引生效。
- [x] 1.2 实现 `internal/rag/chunk.go` 的 `auto`（800/重叠 80，空行 > 换行 > 句末）与 `custom`（100–2000、0–50%）切分，保证偏移可映射回原文、切片严格前进、不产生空切片。
  verify：`internal/rag` 单元测试覆盖空文本、非法模式、custom 越界参数、超长中文、rune 边界与自然断点；另在真实数据上按字节核对，两个切片的 `start_offset`/`end_offset` 截取数据库正文与 `chunk_text` **逐字一致**。

## 2. 入库链路

- [x] 2.1 `upload.go` 在既有事务内写入切片（`split_mode`、`start_offset`/`end_offset`、`chunk_text`、`embed_status='pending'`），失败使上传整体回滚。
  verify：实测上传 905 字节材料返回 201 并写入 2 条切片（`chunk_index` 0/1，偏移 0–622、622–905，正好覆盖全文）；切片插入位于 materials/knowledge_entries 同一事务内，既有故障注入用例（file/extract/material/knowledge/move/before_commit）全部保持通过。
- [ ] 2.2 事务提交后按配置执行嵌入与 Qdrant upsert，成功置 `ready`、失败置 `failed`，材料与知识库记录保持不变。
  verify：**部分通过**。未配置 `EMBEDDING_*` 时实测上传后切片保持 `pending` 且仍返回 201（降级路径正确）。`ready`/`failed` 分支需要课程嵌入网关，尚未实测，故本项不勾选。

## 3. 检索

- [ ] 3.1 实现关键字检索（`MATCH ... AGAINST` + `class_id` 条件）与向量检索（嵌入问句 + Qdrant 按 `class_id` 过滤、余弦阈值 0.35），并在两条都可用时做 RRF(`k=60`) 融合取前 4。
  verify：**部分通过**。`Fuse` 单元测试覆盖双路径排序与单路径顺序；关键字路径实测命中，跨班检索为空。向量路径与"Qdrant 停机返回 503"需要课程嵌入网关才能执行，尚未实测，故本项不勾选。
- [x] 3.2 网关与 Qdrant 客户端（`internal/rag/gateway.go`、`qdrant.go`）只用标准库实现，配置缺失时向量路径自动停用。
  verify：`go.mod`/`go.sum` 未变化；未配置 `EMBEDDING_*` 时 api 正常启动、`VectorEnabled()` 为 false、关键字询问答照常工作；`docker compose exec api wget http://qdrant:6333/collections` 返回 `{"status":"ok"}`，证明内部连通与客户端约定一致。

## 4. 问答接口

- [x] 4.1 新增 `POST /api/ask`：校验问题（400）、按调用者班级检索、取前 4 条、无命中返回固定文案且**不调用对话网关**、有命中则调用对话网关并要求 `[1][2]` 标注。
  verify：实测四类输入——"磁铁能吸木块吗？"→ 200 且回答 `不能，磁铁不能吸引木块。[1]`；"珠穆朗玛峰有多高？"→ 200 且回答 `资料中未找到相关内容。`、`hits: []`（未发生对话调用）；空白问题 → 400 `invalid_request`；匿名 → 401。`parseAsk` 另有单元测试覆盖 13 类畸形输入与长度边界。
- [x] 4.2 返回可回溯引用（材料标题、切片序号、字符区间、摘录），摘录取自 MySQL 切片正文。
  verify：实测引用为 `[1] 小学科学课程资料.md · 切片 1 · 偏移 622–905`，`excerpt` 与数据库 `chunk_text` 完全相同；`hits` 只含调用者班级的切片（B 班调用返回空）。

## 5. 前端与部署

- [x] 5.1 前端新增「知识问答」入口与视图：提问输入、对话流、依据回答、引用卡片；请求经由 `api.ts` 自动带 `Authorization`。
  verify：`tsc --noEmit` 与 `vite build` 通过；真实浏览器实测登录 → 切到知识问答 → 提问，"网页发出的接口调用"为 `GET /api/me, POST /api/login, GET /api/materials, POST /api/ask`，页面渲染出回答与引用卡片，0 个运行时错误。
- [x] 5.2 Compose 增加 `qdrant` 服务与 `qdrant_data` 卷，api 传入 `QDRANT_URL` / `EMBEDDING_*` / `CHAT_*`（均为可选，缺省不影响启动）。
  verify：`docker compose config --quiet` 通过；mysql / qdrant / api / nginx 四个容器均 healthy；api 在不配置任何嵌入项时正常启动。

## 6. 端到端验收

- [x] 6.1 结构校验：`openspec validate add-class-scoped-qa --strict` 通过（4/4 产物）。
- [x] 6.2 真实链路：教师上传一份 A 班材料 → 确认切片与（若已配置）向量入库 → 提问得到带 `[n]` 引用的回答 → 提问材料外的问题得到固定文案 → 用 B 班账号提问拿不到 A 班内容。
  verify：全部在运行中的 Compose 栈实测通过（见 2.1、4.1、4.2 的实际输出）。**未覆盖**：向量路径（缺课程嵌入网关 `EMBEDDING_*`，需拿到后填入 `.env` 重建 api 再复测）与"Qdrant 停机返回 503"。
- [x] 6.3 截图素材：浏览器中以 `teacher_a` 打开知识问答，提一个材料内确有答案的问题，截取包含问题、依据回答与引用卡片的页面。
  verify：已生成 `frontend/test-results/qa-answer.png`（含问题、依据回答与引用卡片）与 `qa-no-evidence.png`（无命中时的固定文案）。用户如需自己截图提交，按同一路径操作即可。
