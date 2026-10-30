# 外部工具（MCP）

这份文档说明"工具"这一侧的完整形状：仓库里有哪些进程、协议长什么样、令牌怎么存、
调用循环怎么收敛，以及哪些行为是刻意不做的。

## 1. 两个进程、一个仓库

| 进程 | 位置 | 作用 |
|---|---|---|
| `api`（宿主） | `backend/` | 登记服务器、连接与发现、生成工具声明、校验参数、带令牌调用、写审计 |
| `mcp`（被调用方） | `mcp-server/` | 单端点 JSON-RPC 2.0 服务，提供 `formula_hint` 与 `unit_convert` 两个工具 |

`mcp-server/` 是独立的 Go module（`campusclaw/mcp-server`），只用标准库，不引用 `backend` 的任何代码。
它由 `deploy/mcp.Dockerfile` 构建，在 Compose 里以服务名 `mcp` 暴露 `8090` 端口，与 `api` 同网。

**为什么把它放在同一个仓库**：验收要能"从零跑起来再看证据"。若被调用方托管在别处，
报告里的每一次调用都无法被别人复现。

## 2. 协议形状

单端点 `POST /mcp`，JSON-RPC 2.0：

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"campusclaw","version":"1.0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"unit_convert","arguments":{"value":3.2,"from":"公里","to":"米"}}}
```

成功响应把结果放在 `result`；失败放在 `error`：

| 情形 | 码 |
|---|---|
| 非法 JSON | `-32700` |
| 缺少 `method` | `-32600` |
| 未知方法 | `-32601` |
| 缺参数、未知工具、参数与结构不符 | `-32602` |

`tools/list` 的每一项含 `name`、`description`、`inputSchema`；`tools/call` 返回
`result.content`（文本数组）与 `result.isError`。

`GET /health` 不需要令牌，只回 `{"status":"ok"}`，不含任何工具或配置信息。

## 3. 令牌怎么走

- `mcp` 服务的令牌**只**从环境变量 `MCP_AUTH_TOKEN` 读取，源码、镜像层与前端产物里都没有它；
  仓库里只有 `.env.example` 的空占位。
- 未配置令牌时 `/mcp` 一律 401：宁可服务不可用，也不允许"忘了配"变成"谁都能调"。
- `api` 侧每台服务器各存一份令牌（不允许共用一把），用 AES-256-GCM 加密后写入
  `mcp_servers.auth_token_cipher`。密钥由 `MCP_TOKEN_KEY` 派生；未配置时由 `TOKEN_SECRET`
  经 HMAC-SHA256 派生一个独立子密钥。
- 接口与页面只回 `has_token`，任何字段都不等于令牌原文。
- **换 `TOKEN_SECRET` 会让已存的令牌解不开**，需要教师重新填一次服务器令牌。

## 4. 连接与发现

`POST /api/mcp/servers/{id}/connect` 依次做三件事：

1. `initialize` 握手（确认对端是 MCP 服务）；
2. `tools/list` 发现（把结果按 `mcp_tools` 的列写入）；
3. **整体替换**缓存：先 `DELETE` 再批量 `INSERT`，本次结果就是全部事实。

任一阶段失败：状态置 `error`、清单清空、`last_error` 留下可读原因、接口返回 502。
不记住失败的发现结果，也不影响第 5 课已有的检索与流式回答路径。

断开（`/disconnect`）与删除同样会清空清单；删除时 `mcp_tools` 随外键级联清理。

## 5. 工具声明从哪来

声明不是手写的：

```
mcp_tools.input_schema ──► tools[i].function.parameters
mcp_tools.name         ──► tools[i].function.name
mcp_tools.description  ──► tools[i].function.description
```

候选集只由**本轮命中的技能**决定：技能「工具」字段里写 `工具名@服务器名`，只有这些引用会进入
`tools`。未命中任何带工具的技能时，请求体里**没有** `tools` 与 `tool_choice` 字段（不是空数组），
因此第 5 课的请求体逐字节不变。

技能保存时会做四类校验（写法 / 属于本班 / 已连接 / 在清单内），全部通过才写入；
运行期只判"当前是否可用"，不重做四类校验。

## 6. 调用循环

```
round = 1
  请求：messages + tools（有候选时）+ tool_choice=auto
  流式读取：累积 content → 逐帧 delta；累积 tool_calls（按 index 拼装 arguments 片段）
  finish_reason == "tool_calls" ?
    是 → 逐次调用：按 inputSchema 校验参数 → 逐工具授权 → 带令牌 tools/call
          → 清洗结果 → 追加 assistant(tool_calls) 与 role:"tool" 消息 → round++
    否 → 结束
```

终止条件有三个，任一满足即停：模型不再提出调用、达到 `MCP_MAX_ROUNDS`、达到 `MCP_MAX_CALLS`。
达到上限**不报错**：下一轮请求不再带 `tools`，让模型用已有信息作答。

| 变量 | 默认 | 说明 |
|---|---|---|
| `MCP_CALL_TIMEOUT` | `10s` | 单次 `tools/call` 的超时 |
| `MCP_MAX_ROUNDS` | `3` | 一次提问内最多几轮工具调用 |
| `MCP_MAX_CALLS` | `4` | 单轮最多执行几次调用 |
| `MCP_TOKEN_KEY` | 空 | 令牌加密子密钥；留空由 `TOKEN_SECRET` 派生 |

## 7. 工具输出被当作不可信文本

四道处理，缺一不可：

1. **位置**：结果只进 `role:"tool"` 消息，绝不拼进系统消息；系统消息里的边界句保留。
2. **清洗**：进上下文前扫一遍指令式句式（"忽略以上指示"、"ignore previous" 等）；
   命中则**整条丢弃**并换成一句固定说明——不删单句，因为被改过的段落容易自相矛盾。
3. **授权**：逐工具判断。保存技能时连通、调用时已断开，这次调用记为 `denied`，回答不含该工具输出。
4. **配额**：轮次与次数上限由宿主强制，与模型的判断无关。

单条结果截断 800 字，与资料切片同口径。

## 8. 失败与降级

| 情形 | 状态 | 对使用者 |
|---|---|---|
| 连接失败 | 502 `upstream_error` | 服务器页显示"连接失败 + 原因"，问答不受影响 |
| 服务器已断开时调用 | 调用记 `denied` | 回答继续，`meta.tool_calls` 里能看到被拒绝 |
| 参数不合结构 | 调用记 `error`，不发远端请求 | 失败原因作为工具消息回传，模型有机会修正 |
| 远端超时 / 非 2xx / `isError` | 调用记 `error` | 同上 |
| 达到轮次或次数上限 | 不报错 | 停止调用，用已有信息作答 |

## 9. 刻意不做的事

- 不做工具市场、不做 OAuth：令牌由教师填写。
- 不做工具级权限矩阵：本课程按"班级 + 是否已连接"授权即可。
- 不缓存工具结果：每次提问都真实调用，否则审计里的"这次到底发生了什么"就不可信。
