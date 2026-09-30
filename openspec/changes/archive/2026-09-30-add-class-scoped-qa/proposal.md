# Proposal

## Why

0930 课堂任务要求把"基于知识库的 AI 问答"做进 CampusClaw：用户就本班材料提问，系统先在本班范围内检索，再用大模型基于检索结果生成简短回答，并标出 `[1]`、`[2]` 的出处，可以回溯到材料标题、切片序号与字符偏移。

仓库里第 4 课的变更 `add-traceable-vector-retrieval` 只写了规约、没有实现。本次课堂任务按"先做通问答最小闭环"的范围落地它的第一片：**切分 → 检索 → 生成带引用的回答**，把 keyword/vector/hybrid 三模式的完整形态与独立检索页留给后续变更。

## What Changes

- 新增迁移 `000003_knowledge_chunks`：切片正文存 MySQL `knowledge_chunks`（`material_id`/`class_id`/`chunk_index`/`start_offset`/`end_offset`/`chunk_text`/`split_mode`/`embed_status`），切片序号在材料内唯一，并与材料保持班级一致。
- 教师上传材料时按 `auto`（默认 800 字、重叠 80，优先空行/换行/句号断开）或 `custom`（显式 100–2000 字、重叠 0–50%）切分，切片在同一事务内随材料与知识库记录一起写入。
- 检索新增两条可独立启用的路径，`/api/ask` 按配置自动选择：
  - **关键字**：MySQL 8.0 的 `FULLTEXT ... WITH PARSER ngram`（`ngram_token_size=2`），始终可用，不需要外部服务。
  - **向量**：调用嵌入网关生成问句与切片向量，存入 Qdrant 集合 `campusclaw_chunks`（余弦），按 `class_id` 过滤后回表取正文。仅在配置了 `EMBEDDING_*` 时启用。
  - 两条都可用时按名次 RRF 融合（`k=60`），取前 4 条。
- 新增 `POST /api/ask`：接收 `{question}`，按**会话班级**检索；命中则调用对话网关生成简短回答并要求用 `[1]`/`[2]` 标注出处；**没有命中时直接返回固定文案且不调用模型**；返回体带可回溯的引用（材料标题、切片序号、字符区间、摘录）。
- 前端新增「知识问答」页：提问输入、对话流、依据回答与引用卡片；材料内容与班号仍只来自 token。
- 模型网关只从服务端调用，密钥不进浏览器；未配置嵌入网关时 `/api/ask` 自动降级为关键字检索，而不是报错。

## Capabilities

### New Capabilities

- `knowledge-qa`: 本班范围内的正文切片、检索与带引用的 AI 问答。

### Modified Capabilities

无。

## Impact

- 后端：新增 `internal/rag`（切分 / 网关客户端 / Qdrant 客户端）与 `internal/httpapi/ask.go`；`handler.go` 增路由；`upload.go` 增切片写入；`config` 增网关与向量库配置；新增迁移 `000003`。
- 部署：`compose.yaml` 增加 `qdrant` 服务与卷，并向 api 传入 `EMBEDDING_*` / `CHAT_*` / `QDRANT_URL`。
- 前端：`App.tsx` 增加导航与问答视图，`api.ts` 增加 `ask()`；沿用现有样式体系。
- 依赖：**不新增 Go 依赖**——嵌入/对话网关与 Qdrant 都用标准库 `net/http` + `encoding/json` 直接调用。
- 数据：`knowledge_chunks` 为新表；已存在的材料需要重新上传或后续补一次回填才算入索引。

## Non-goals

不做独立「知识检索」页面与 `POST /api/search`；不做 `hierarchy` 按标题切分；不做切片策略切换后的显式重建索引入口；不做流式回答（SSE）、多轮会话记忆、提示词与技能（第 5 课）；不做 token 吊销与限流。不把课表练习需求混入 CampusClaw。
