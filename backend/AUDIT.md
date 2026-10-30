# 模型调用审计

审计不是附属功能，而是验收手段：五个验证问题都靠这张表回答。
这份文档说明记录了哪些字段、哪些是派生列、派生规则是什么，以及怎么用一条 SQL 取到证据。

## 1. 两张表

| 表 | 一行是什么 | 粒度 |
|---|---|---|
| `llm_calls` | 一次对模型网关的调用 | 一次提问可能产生多行（工具循环每轮一行），`round_no` 区分 |
| `tool_calls` | 一次外部工具调用 | 一次提问可能产生多行，`round_no` + `llm_call_id` 回指产生它的那一轮 |

`llm_calls` **不挂业务外键**：会话或应用被删除后，证据必须留下来。

## 2. 字段与来历

| 字段 | 来历 |
|---|---|
| `request_json` | **真正发出的字节**。由网关层在 `json.Marshal` 之后原样交给审计，不重建、不摘要 |
| `response_text` / `response_json` | 上游正文与一份响应摘要（正文 + `finish_reason` + 提议的调用） |
| `system_prompt` | **派生**：`messages` 里第一条 `role=system` 的内容 |
| `user_prompt` | **派生**：`messages` 里最后一条 `role=user` 的内容 |
| `context_chars` | **派生**：注入字符数，见下一节 |
| `message_count` | 实际发出的消息条数 |
| `skill_name` | 本轮命中的技能名（未命中为空） |
| `tool_def_count` | 本次请求带了几条 `tools` 声明 |
| `tool_call_count` | 模型提出了几次调用 |
| `finish_reason` / `http_status` / `status` | 上游结束原因、HTTP 码、成功或失败 |
| `prompt_tokens` / `completion_tokens` | 网关在最后一帧 `usage` 里给出的用量 |
| `latency_ms` | 本次调用的墙钟耗时 |

三个派生列（系统消息、用户消息、注入字符数）都由**实际发送的消息**算出，
不由调用方另行传入——否则"发出去的"与"记下来的"可能不是一回事。

## 3. `context_chars` 的确切口径

只数"随检索与工具带进来的内容"：

```
context_chars = Σ 所有 role=="tool" 消息的字符数
              + 最后一条 user 消息里「资料：」到「问题：」之间的字符数
```

历史对话不计入：它不属于"这次给模型的资料"。两边标记由
`chat.BuildMessages` 与 `internal/audit` 共享，`internal/audit/derive_test.go` 用真实
构建结果锁定这个约定，任何一边改动都会让测试变红。

## 4. 失败也记

| 情形 | 记录 |
|---|---|
| 网关非 2xx | `status=failed`、`http_status=4xx/5xx`、`error_text` 写状态码 |
| 连接失败 / 超时 | `status=failed`、`http_status=0`、`error_text` 写原因 |
| 审计自身写入失败 | 只写服务端日志，**不改变回答状态码** |

`request_json` 里可能出现提示词与资料，但不会有网关 API Key 或工具令牌：
密钥只在 HTTP 头里，而请求头从不落库。

## 5. 查询接口

| 接口 | 作用 |
|---|---|
| `GET /api/llm-calls?limit=` | 列表：教师看本班，学生只看自己 |
| `GET /api/llm-calls/{id}` | 单条：完整 `request_json`、响应、附带的工具调用 |
| `GET /api/llm-calls/{id}/context-size` | 第 3 问的对比：`source_chars` 与 `context_chars` |

越权与其他班级的访问一律返回与"不存在"完全一致的 404。

## 6. 五个问题的取数方式

| 问题 | 一句话结论 | 取证据 |
|---|---|---|
| **Q1** 传给模型什么、返回什么？模板传了吗？ | 请求体是 `{model,messages,temperature,max_tokens,stream,stream_options}`；模板以**渲染后全文**进入 `messages[0].content`，编号与模板原文都不传 | `SELECT request_json, system_prompt FROM llm_calls WHERE id=?`；页面「调用审计 → 查看」 |
| **Q2** 知识库是全量传的吗？ | **否**：只注入命中的前 4 条切片，每条 ≤800 字 | `GET /api/llm-calls/{id}/context-size` → `source_chars` 与 `context_chars` 不等 |
| **Q3** 技能内容传了多少？ | 只传**命中的那一个**技能的 `name` + 引导规程（`description`）；原文 `source_text` 不传 | `skill_name` 列 + `system_prompt` 里「当前技能：」段 |
| **Q4** 技能的执行逻辑？ | 服务端**词法计分路由**（不调模型）→ 注入规程 → 声明的工具进入工具循环 | `skill.Score` 单测 + 启用/停用前后的两条 `system_prompt` |
| **Q5** 接 MCP 后参数有何变化？ | 请求体新增 `tools` 数组（`parameters` 即发现结果里的 `inputSchema`）与 `tool_choice`；工具结果以 `role:"tool"` + `tool_call_id` 回到 `messages`；一次提问可能多轮 | `SELECT round_no,tool_def_count,tool_call_count,request_json FROM llm_calls WHERE conversation_id=? ORDER BY id` |

### 直接可用的 SQL

```sql
-- 一次提问的全部轮次
SELECT id, round_no, tool_def_count, tool_call_count, finish_reason, context_chars
FROM llm_calls WHERE conversation_id = ? ORDER BY id;

-- 这次提问的工具调用
SELECT round_no, server_name, tool_name, status, latency_ms, result_excerpt, error_text
FROM tool_calls WHERE conversation_id = ? ORDER BY id;

-- 系统提示词首段与是否含模板编号（应为 0）
SELECT LEFT(system_prompt, 80) FROM llm_calls WHERE id = ?;
SELECT LOCATE('template_id', request_json) FROM llm_calls WHERE id = ?;
```

## 7. 已知限制

- 审计按"验收期设计"保存完整请求体，长期运行需要采样与 TTL；这属于 Non-goals。
- `response_json` 是摘要而不是上游原始 SSE 字节：证据主干在 `request_json`，
  响应侧要回答的是"拿回了什么、为什么停"。
- 记录不保存请求头与任何密钥；因此"当时带的是哪个 Authorization"无法从审计复原。
