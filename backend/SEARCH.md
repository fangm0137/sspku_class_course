# 检索与问答

第 4 课的检索能力：入库时切分并向量化，检索支持关键字、向量与混合三种模式，命中可回溯到原文，
问答只用命中切片生成并标注出处。实现位于 `internal/rag/`（切分、融合、网关与向量库客户端）
与 `internal/httpapi/search.go`、`ask.go`。

## 数据布局

| 位置 | 内容 |
| --- | --- |
| MySQL `knowledge_entries.body_text` | 上传原文，切分不改写它 |
| MySQL `knowledge_chunks` | `chunk_text` 与 `char_start`/`char_end`（预处理后文本的字节区间）、`strategy`、`index_status` |
| Qdrant `campusclaw_chunks` | 定长浮点向量 + 标识 payload（`class_id`/`material_id`/`knowledge_entry_id`/`chunk_id`/`chunk_index`），**不存正文** |

向量主键、`knowledge_chunks.id` 与 `payload.chunk_id` 三者相同，因此检索一律按主键回 MySQL 取正文，
摘录永远来自 `chunk_text`。集合按 `EMBEDDING_DIM` 建立（当前 `course-embedding` 为 2048，Cosine），
更换嵌入模型必须重建索引。

## 切分策略

| 策略 | 参数 | 行为 |
| --- | --- | --- |
| `auto`（默认） | 固定 800 字节、重叠 80 字节 | 优先在空行、换行、句末标点断开；重叠不足一字时对齐到字符边界 |
| `custom` | `size` 100–2000、`overlap` ≤ size/2 | 同上，另可选移除链接/邮箱与折叠连续空白 |
| `hierarchy` | 无参数 | 按 `#`/`##`/`###` 分章，标题保留在章内；单章过长时再按 auto 窗口细分 |

预处理只作用于待切分与待嵌入文本；启用后 `char_start`/`char_end` 的基准是**预处理后**的文本，
未启用时与 `body_text` 完全一致。

## 接口

### GET /api/search

需要登录。查询串参数：`q`（必填，≤500 字符）、`mode`（`keyword`/`vector`/`hybrid`，缺省 `hybrid`）、
`limit`（1–20，缺省 4）。请求里出现的班级编号一律被忽略，过滤条件只取自会话。

```json
{
  "mode": "hybrid",
  "hits": [
    {"material_id": 5, "title": "小学科学课程资料.md", "chunk_id": 12, "chunk_index": 1,
     "char_start": 622, "char_end": 905, "excerpt": "……", "score": 0.032266}
  ]
}
```

`score` 只是本次模式的排序分（关键字为全文相关度、向量为余弦相似度、混合为 RRF 名次分），
不是相关性阈值，不跨模式比较。

失败：空查询/非法模式/超长查询 `400`；未登录 `401`；`vector` 与 `hybrid` 在向量路径不可用时 `503`。

### POST /api/materials/{id}/reindex

仅教师，且只能重建本班材料（其他班级与不存在的材料同为 `404`）。请求体可选，缺省即 `auto`：

```json
{"strategy":"custom","size":600,"overlap":60,"collapse_whitespace":true}
```

行为是**显式重建**：先删除该材料的旧向量与旧切片，再按本次策略重新切分、重新嵌入。
返回 `{"material_id":5,"strategy":"custom","chunks":3}`。参数非法 `400`；依赖不可用 `503`。
未重建的旧切片保持原样，因此历史材料不会自动按新策略重新切分。

### POST /api/ask

需要登录。请求体 `{"question":"..."}`；客户端附带的 `system` 消息与历史轮次一概丢弃，
系统提示由服务端写入，检索只以本轮提问发起，不做流式输出。

```json
{
  "answer": "磁铁不能吸引木块。[1]",
  "hits": [{"material_id":5,"title":"小学科学课程资料.md","chunk_id":12,"chunk_index":1,
            "char_start":622,"char_end":905,"excerpt":"……","citation":1}],
  "citations": [{"material_id":5,"title":"小学科学课程资料.md","chunk_id":12,"chunk_index":1}]
}
```

`hits` 是按 `[n]` 顺序排列的可回溯摘录，`citations` 是同一顺序的紧凑标识。
没有命中时 `answer` 固定为「资料中未找到相关内容。」、`hits` 与 `citations` 为空，且**不调用对话网关**。

## 依赖故障与降级

| 场景 | `GET /api/search` | `POST /api/ask` |
| --- | --- | --- |
| 未配置 `EMBEDDING_*` | `keyword` 正常；`vector`/`hybrid` `503` | 自动只用关键字路径，照常作答 |
| Qdrant 停机 | `keyword` 正常；`vector`/`hybrid` `503` | 关键字已有候选则继续作答，否则 `503` |
| 对话网关不可用 | 不涉及 | 有命中时 `503`，不返回编造回答 |
| 嵌入在入库时失败 | 该切片 `index_status='failed'`，不写向量 | 不会被检索到；材料与正文保留 |

检索接口按第 4 课规约对 `hybrid` 采取严格语义（两条路径都要能执行），问答接口按 `knowledge-qa`
基线的降级语义处理，两者共用同一套检索实现，只是对"路径故障"的处置不同。

## 重建历史材料（backfill）

第 3 课入库的材料没有切片，需要跑一次显式重建。命令在 `backend` 或容器内执行：

```bash
# 全部材料
docker compose run --rm --no-deps --entrypoint /app/reindex api
# 指定材料
docker compose run --rm --no-deps --entrypoint /app/reindex api 4 5
```

命令按 `auto` 策略重建，失败不自动重试；未配置 `EMBEDDING_*` 时只写关键字切片并给出提示。
