# Design

## Context

第 3 课已经完成"教师上传 → 原文件 + 材料 + 提取文本入库"的同步链路（`upload.go` 单事务 + 提交后补偿），第 4 课只留下规约。本变更落地其中"能问答"的一片：切分入库、检索、生成带引用的回答。

约束：Go 1.26 标准库 + 既有 3 个依赖，**不新增依赖**；嵌入与对话网关都要走 HTTP，Qdrant 也是如此。api 必须能在**没有任何模型网关配置**时正常启动，否则本地环境一换配置就起不来。

## Goals / Non-Goals

**Goals**

- 上传即可检索：切分、入库与问答在同一次迭代内闭环。
- 回答可回溯：每个 `[n]` 都能定位到材料标题 + 切片序号 + 字符区间，摘录取自数据库而不是模型输出。
- 班级边界不弱化：班级只取自 token 对应的账号记录。
- 无嵌入网关时仍可用（关键字路径），有配置时自动升级为混合检索。

**Non-Goals**

- 独立检索页、`POST /api/search`、`hierarchy` 切分、切片策略切换后的显式重建入口。
- 流式回答、多轮记忆、提示词与技能（第 5 课）；token 吊销、限流。

## Decisions

### D1. 切片表与偏移基准

```sql
CREATE TABLE knowledge_chunks (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  material_id BIGINT UNSIGNED NOT NULL,
  class_id BIGINT UNSIGNED NOT NULL,
  chunk_index INT UNSIGNED NOT NULL,
  start_offset INT UNSIGNED NOT NULL,
  end_offset INT UNSIGNED NOT NULL,
  chunk_text LONGTEXT NOT NULL,
  split_mode ENUM('auto','custom') NOT NULL,
  embed_status ENUM('pending','ready','failed') NOT NULL DEFAULT 'pending',
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uq_chunks_material_index (material_id, chunk_index),
  KEY idx_chunks_class_id (class_id),
  CONSTRAINT fk_chunks_material_class FOREIGN KEY (material_id, class_id) REFERENCES materials (id, class_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs;
```

- 偏移以**预处理后文本**（去掉首个 BOM 的提取文本，即 `knowledge_entries.extracted_text`）为基准，用 Go 的**字节偏移**。字符区间对外按字节区间展示，与切片正文一一对应，可逐字复现。
- `split_mode` 用 ENUM 而不是自由字符串，避免写入未实现的策略。
- `embed_status` 让"切片已入库但向量还没写"可被区分，重试时不重复嵌入已成功的切片。

### D2. 切分算法（`internal/rag/chunk.go`）

`auto`：目标 800 字节、重叠 80 字节。从当前位置向后寻找**不超过目标长度**的最后一个断开点，优先级为

1. 空行 `\n\n`
2. 换行 `\n`
3. 句末标点 `。！？；!?;.` 之后
4. 都没有时按目标长度硬切（UTF-8 边界安全回落）

下一片的起点 = 本片终点 − 重叠长度，并保证严格前进（起点必须大于上一片起点）以避免死循环。`custom` 使用显式 size/overlap，校验 100–2000 与 0–50%，其余逻辑与 `auto` 相同。空文本或长度为零的切片直接判为失败，避免写入无意义行。

选择字节而不是 rune 计数：偏移要能和数据库里的原文做字节级比对，展示时再按 UTF-8 解码；重叠按字节计在中文文本下偏小，但不影响正确性。

### D3. 关键字路径（始终可用）

`knowledge_chunks` 上建 `FULLTEXT KEY ft_chunks_text (chunk_text) WITH PARSER ngram`；MySQL 8.0.46 的 `ngram_token_size` 取默认 2，与第 4 课规约一致。查询用

```sql
SELECT c.id, c.material_id, c.chunk_index, c.start_offset, c.end_offset, c.chunk_text,
       m.original_name, MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE) AS score
FROM knowledge_chunks c JOIN materials m ON m.id = c.material_id
WHERE c.class_id = ? AND MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE)
ORDER BY score DESC, c.id ASC LIMIT ?
```

班级过滤写在 SQL 的 `WHERE` 里，不靠调用方。`ngram` 对中文按 2 字滑窗，对"磁铁""木块"这类词直接可用，不需要停止词表。

### D4. 向量路径（配置后启用）

- 嵌入：`POST {EMBEDDING_BASE_URL}/embeddings`，请求体只含 `{"model":..., "input":[...]}`；读取 `data[i].embedding`，长度必须等于 `EMBEDDING_DIM`，否则视为失败。
- Qdrant：`PUT /collections/campusclaw_chunks`（`{"vectors":{"size":DIM,"distance":"Cosine"}}`，已存在则忽略）、`PUT /collections/campusclaw_chunks/points?wait=true`（点 id = `knowledge_chunks.id`，payload 只放 `class_id`、`material_id`、`chunk_id`、`chunk_index`，不重复存正文）、`POST /collections/campusclaw_chunks/points/query` 按 `class_id` 过滤检索。
- 入库时机：切片在事务内写入后，事务外做嵌入与 upsert；成功把 `embed_status` 置 `ready`，失败置 `failed`，**不删除材料**。检索只取 `embed_status='ready'` 的切片参与向量路径。
- 余弦相似度按 `1 - distance` 换算后与 0.35 阈值比较。

Qdrant 与嵌入网关全部用 `net/http` 直接调用 REST，不引入客户端库，理由与 JWT 那次相同：依赖面越小，构建与离线环境越可控。

### D5. 融合与阈值

两条路径各自取前 20 条候选，按名次做 RRF：`score = Σ 1/(60 + rank)`，按分数降序、切片 id 升序稳定排序，取前 4 条。只用一条路径时不做融合，直接取该路径排序结果。关键字路径的候选**不做相似度阈值过滤**（全文命中本身就是依据），向量候选要求余弦相似度 ≥ 0.35。

依赖故障的处理：向量路径失败时，只要关键字路径仍有候选就继续作答（并记录日志）；两条都拿不到候选才返回 503。这样课程网关抖动不会让整个问答不可用，同时也不会把"依赖挂了"伪装成"资料里没有"。

### D6. `/api/ask` 流程与提示词

1. 解析并校验 `question`（去空白后非空，长度上限 500）。
2. 检索（D5），取前 4 条。
3. 命中为空 → 返回 200、`hits: []`、`answer` 为固定文案「资料中未找到相关内容。」，**直接返回，不调用对话网关**。
4. 命中非空 → 组装 system + user：system 规定"只依据给定片段回答；用 `[1]`、`[2]` 标注依据；片段里没有的不要编造；回答尽量简短"；user 里按序号附上片段正文与来源标题。
5. 取回复文本，去掉首尾空白；为空视为网关失败。
6. 返回 `{"answer": "...", "hits":[{"material_id","title","chunk_index","start_offset","end_offset","excerpt","citation"}]}`，`excerpt` 取自 MySQL 的 `chunk_text`，不采信模型复述。

不做流式；不做多轮上下文；不做答案缓存。

### D7. 配置与降级

| 变量 | 必需 | 说明 |
| --- | --- | --- |
| `QDRANT_URL` | 否 | 缺省 `http://qdrant:6333`；未配置嵌入时不起作用 |
| `EMBEDDING_BASE_URL` / `_API_KEY` / `_MODEL` / `_DIM` | 否 | 四者齐全才启用向量路径 |
| `CHAT_BASE_URL` / `_API_KEY` / `_MODEL` | 是（问答用） | 缺任一项时 `/api/ask` 返回 503 并说明未配置 |

- **api 启动不因为缺这些而失败**：它们是可选配置，与 `TOKEN_SECRET` 这类必需项区别对待。
- 使用时才校验：`/api/ask` 需要对话网关；只有关键字路径可用时也能问答。

### D8. 前端

沿用现有单页布局，在侧栏加"知识问答"入口；问答视图包含提问输入、对话流（用户气泡 + 依据回答卡片）与引用卡片（`[n] 材料标题 · 切片 i · 偏移 a–b` + 片段正文）。错误按既有 `ApiError` 文案展示；无命中时展示固定文案与"未检索到依据"的提示。请求头仍由 `api.ts` 统一附加 `Authorization`。

## Risks / Trade-offs

| 风险 | 说明 | 缓解 |
| --- | --- | --- |
| 上传变慢 | 嵌入是同步外部调用 | 嵌入失败只置 `failed`，不影响上传成功；后续可加异步任务（第 8 课） |
| ngram 对超短查询弱 | 单字查询命中差 | 阈值与排序都保留，问答里问题通常是词或短句 |
| 历史材料没有切片 | 老材料在新表里没有记录 | 重新上传即可；补回填入口不在本次范围 |
| 字节偏移对多字节文本不直观 | 偏移按字节 | 与切片正文一一对应、可复现；展示层同时给切片序号 |

## Migration Plan

1. 执行 `migrate up` 建 `knowledge_chunks` 与全文索引。
2. 配置 `QDRANT_URL` 与（可选）`EMBEDDING_*`、`CHAT_*`，重建 api 镜像；Compose 增加 `qdrant` 服务与 `qdrant_data` 卷。
3. 教师重新上传需要被问答的材料，切片与向量随之生成。
4. 回滚：`migrate down 1` 删除切片表；代码回退后上传链路不再写切片，材料与知识库数据不受影响。

## Open Questions

- 是否把嵌入改成上传后的异步任务？（第 8 课异步任务与消息队列）
- 是否需要"重建索引"入口以支持切换切分策略？（第 4 课规约里已列为要求，留给后续变更）
