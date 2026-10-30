# Tasks

本变更用于个人项目验收：五项能力补齐 + 五项验证取证。任务按依赖顺序排列，**每项完成并执行 verify 后才勾选**。

## 1. 数据模型与配置

- [ ] 1.1 新增迁移 `000006_app_platform`：建 `prompt_templates`、`mcp_servers`、`mcp_tools`、`llm_calls`、`tool_calls`；给 `tutor_agents` 增加 `prompt_template_id`（同班复合外键）与 `builtin_key`（唯一）；写 up/down。
  verify：真实 MySQL 8.0 上两次 `up`，第二次输出 `no change`；`SHOW CREATE TABLE` 核对非空列、唯一键、复合外键与 CHECK；写入违例（重名模板、跨班模板引用）被数据库拒绝。
- [ ] 1.2 配置项：`MCP_AUTH_TOKEN`（MCP 服务端自身令牌）、`MCP_CALL_TIMEOUT`（默认 10s）、`MCP_MAX_ROUNDS`（默认 3）、`MCP_MAX_CALLS`（默认 4）、`MCP_TOKEN_KEY`（可选，缺省由 `TOKEN_SECRET` 派生）。全部可选，缺失时其余能力不受影响。
  verify：不配置任何新变量时服务仍能启动、第 3–5 课接口全部正常；配置非法值只报告变量名。

## 2. 提示词模板

- [ ] 2.1 `internal/prompt`：模板渲染器。支持 `{{app_name}}`、`{{today}}`、`{{question}}`；**单次替换、不递归**；未知占位符原样保留。
  verify：单元测试覆盖三占位符替换、未知占位符保留、提问内含 `{{today}}` 不被二次替换、空模板体被拒；`go test ./internal/prompt/...` 通过。
- [ ] 2.2 模板 CRUD 接口与存储：班内名称唯一、1–4000 字、教师可写、跨班 404、学生 403、被引用时删除返回 409、停用模板仍可被已引用应用渲染。
  verify：用 curl 逐条打 5 类请求（创建/改名/跨班读/学生写/被引用删除），状态码分别为 201/200/404/403/409。
- [ ] 2.3 应用引用模板：`tutor_agents.prompt_template_id` 可空；为空回落自由文本、再回落默认角色；设为已停用模板返回 400。
  verify：三个应用分别配"引用模板""仅自由文本""两者皆空"，各自提问后从 `llm_calls.system_prompt` 核对首段来源。

## 3. 内置应用与内置技能

- [ ] 3.1 `cmd/seed-apps`：以 `builtin_key` 幂等写入数学助教、语文助教、通用答疑三个内置应用，各自绑定对应模板、学科技能与材料范围；与样板不一致时报冲突不覆盖。
  verify：空库首跑输出 `app seed initialized`，三行 `builtin_key` 非空；重跑输出 `no change`；手工改动后重跑报冲突且改动保留。
- [ ] 3.2 内置身份的约束：内置应用删除返回 409；内置技能删除返回 409；两者均可启停，停用后行为可观察。
  verify：`DELETE /api/agents/{内置id}` 与 `DELETE /api/skills/{内置id}` 均 409；停用「数学解题引导」后同一提问的 `system_prompt` 不再含"不要写出最终答案"。
- [ ] 3.3 技能列表区分内置与自建（`builtin` 布尔）。
  verify：`GET /api/skills` 中两条记录 `builtin=true`，上传的技能为 `false`。

## 4. 技能文件上传

- [ ] 4.1 `POST /api/skills/upload`：multipart 单文件、字段 `file`、仅 `.txt`/`.md`、≤64 KiB、UTF-8；与 JSON 创建共用同一套解析与校验。
  verify：合法 `.md` → 201；`.pdf`、超 64 KiB、缺元数据字段分别 → 400；缺字段时响应含行号；两次拒绝后技能总数不变。
- [ ] 4.2 上传后的技能立即可绑定并在下一次提问参与路由。
  verify：上传 → 绑定到应用 → 提问命中，`meta.skill.name` 为该技能名。

## 5. MCP 服务端（同仓 `mcp-server/`）

- [ ] 5.1 新建 `mcp-server/`（Go，标准库）：`POST /mcp`（JSON-RPC 2.0 单端点）与 `GET /health`；令牌只从 `MCP_AUTH_TOKEN` 读取；缺令牌/错误令牌 401。
  verify：不带令牌 401、错误令牌 401、正确令牌 `initialize` 返回 `serverInfo`；`/health` 无令牌 200 且不泄露工具信息。
- [ ] 5.2 实现 `initialize`、`tools/list`、`tools/call`；工具 `formula_hint`（公式提示，不给最终答案）与 `unit_convert`（单位换算），名称小写开头无连字符；`tools/list` 每项含 `name`/`description`/`inputSchema`；`tools/call` 返回 `result.content` 文本数组与 `isError`。
  verify：`tools/list` 返回两个工具及完整 `inputSchema`；对两者各发一次 `tools/call` 均返回 `content[0].type=text` 且 `isError=false`。
- [ ] 5.3 JSON-RPC 错误语义：非法 JSON `-32700`、未知方法 `-32601`、参数缺失 `-32602`。
  verify：三种请求分别返回对应错误码，且响应中不含 `result.content`。
- [ ] 5.4 Compose 增加 `mcp` 服务（与 api 同网）、`MCP_AUTH_TOKEN` 注入；Dockerfile 与 `.env.example` 同步。
  verify：`docker compose up -d --wait mcp` 后容器 healthy；从 api 容器 `wget -qO- http://mcp:8090/health` 得 200。

## 6. MCP 主机侧

- [ ] 6.1 登记与列表：名称班内唯一、Base URL 仅 http/https 且不含账号密码、令牌 AES-GCM 加密落库、响应只回 `has_token`。
  verify：登记后 `SELECT auth_token_cipher FROM mcp_servers` 为密文且不等于原文；`GET /api/mcp/servers` 响应中检索不到令牌原文。
- [ ] 6.2 连接：`initialize` 握手 → `tools/list` 发现 → **整体替换**缓存 → `status=connected`；失败置 `error`、清空清单、返回 502，问答主路径不受影响。
  verify：指向自身 MCP 服务连接成功且工具清单可见；连续连接两次后清单不重复；指向不可达地址返回 502 且随后提问仍 200。
- [ ] 6.3 断开与删除：断开清空缓存、状态回 `disconnected`；删除级联清理 `mcp_tools`。
  verify：断开后 `GET /api/mcp/servers/{id}/tools` 为空；已引用其工具的技能保存返回 400。
- [ ] 6.4 权限与隔离：全部写操作仅教师（学生 403）、跨班一律 404。
  verify：三账号矩阵逐条打接口，状态码符合预期。

## 7. 工具声明与调用循环

- [ ] 7.1 技能「工具」语法扩展与配置期校验：接受 `knowledge_search` 与 `工具名@服务器名`；保存时按四类条件（格式/属于本班/已连接/在清单内）校验，失败返回 400 并指出失败类别。
  verify：合法引用 200；五条非法引用（无 `@`、多 `@`、服务器不存在、他班、未连接）各 400，错误文案指向具体引用。
- [ ] 7.2 工具声明生成：把本轮候选集（命中技能声明的引用）按发现结果转成 `tools` 数组，`parameters` 直接取自 `mcp_tools.input_schema`；**无候选时不带 `tools` 与 `tool_choice` 字段**。
  verify：命中工具时请求体 `tools` 长度等于技能声明的工具数；`tools[0].function.parameters` 与 `tools/list` 的 `inputSchema` 逐字段一致；未命中时 `grep -c '"tools"' request_json` 为 0。
- [ ] 7.3 调用循环：解析 `tool_calls`（含**流式按 `index` 增量拼装** arguments 片段）；按 `inputSchema` 校验参数；逐工具授权；带令牌 `tools/call`（超时 10s）；把 assistant 的 `tool_calls` 与 `role:"tool"`+`tool_call_id` 消息追加后再次请求，直到 `finish_reason` 非工具调用。
  verify：两步任务能连续发起两轮，第二轮后 `finish_reason=stop`；把 `MCP_MAX_ROUNDS=1` 后同一问题只发一轮且回答仍 200。
- [ ] 7.4 校验失败与上限降级：参数不合格时不发出 `tools/call`，把原因作为工具消息回传给模型；达到轮次或次数上限即停止调用并让模型用已有信息作答，不报错。
  verify：构造缺必填参数的调用（用单测打桩）→ 无远端请求、记 `error`、模型收到失败原因；`MCP_MAX_CALLS=1` 时第二轮被截断且回答完整。
- [ ] 7.5 安全四道：工具结果只进 `role:"tool"` 消息（不进系统消息）；指令式句式命中则整条丢弃并替换为固定说明；逐工具授权；轮次与次数配额。
  verify：`system_prompt` 中检索不到工具输出片段；构造含"忽略以上指示"的工具返回 → 该条被整条替换、模型未读到原文；断开服务器后调用记 `denied`。
- [ ] 7.6 `meta.tool_calls`：`[{ref, status, latency_ms, text}]`，`ok`/`denied`/`error` 三态；**无调用时该字段整体省略**；先于首个 `delta` 送达，多轮按顺序推送。
  verify：正常/断开/超时三种情形分别得 `ok`/`denied`/`error`，三次提问均 200；无工具的提问其 `meta` 中不含 `tool_calls` 键；前端在首个正文片段前已收到工具信息。

## 8. 模型调用审计

- [ ] 8.1 `llm_calls` 写入：每次调用一行，记录 `round_no`、请求体原文、响应正文、`finish_reason`、状态、HTTP 码、耗时、token 用量，以及派生的 `system_prompt`、`user_prompt`、`message_count`、`context_chars`、`skill_name`、`tool_def_count`、`tool_call_count`。
  verify：一次两步提问产生两条记录（`round_no` 1 与 2）；`request_json` 可解析且含 `model/messages/temperature/max_tokens/stream`；第二轮记录的 `message_count` 比第一轮大（多了 assistant 与 tool 消息）。
- [ ] 8.2 失败也记：非 2xx、超时、连接失败写 `status=failed` 与错误摘要，不含密钥。
  verify：把 `CHAT_BASE_URL` 指向不可达地址后提问，审计出现 `failed` 记录且响应中无密钥。
- [ ] 8.3 审计是旁路：写失败不影响回答。
  verify：临时把 `llm_calls` 改名后提问，回答仍 200 且完整，日志出现审计失败行。
- [ ] 8.4 工具调用审计：`tool_calls` 表记录服务器名、工具名、`round_no`、参数、状态、结果摘要或错误、用户、会话、时间。
  verify：一次成功调用留下一条 `ok` 且 `result_excerpt` 非空；制造 `denied` 与 `error` 各一条，三者可区分。
- [ ] 8.5 查询接口：`GET /api/llm-calls`（教师本班 / 学生本人）、`GET /api/llm-calls/{id}`、`GET /api/llm-calls/{id}/context-size`（返回 `source_chars` 与 `context_chars`）。
  verify：三账号矩阵权限正确；`context-size` 对一份 905 字材料返回 `source_chars=905` 而 `context_chars` 明显更小。

## 9. 前端

- [ ] 9.1 「AI 应用」视图：列出本班应用（含内置标记）、组成视图（模板名/技能/材料数/工具引用）、编辑模板引用与绑定。
  verify：浏览器中能看到三个内置应用；组成视图与实际提问行为一致。
- [ ] 9.2 「提示词模板」视图：列表、编辑、占位符说明、被引用时的删除提示。
  verify：新建模板 → 绑定到应用 → 提问 → 在审计页看到渲染结果。
- [ ] 9.3 「MCP 工具」视图：登记服务器、连接/断开、查看发现到的工具（含参数结构）、显示令牌"已配置/未配置"、查看最近调用记录。
  verify：连接后能看到 `formula_hint` 与 `unit_convert` 的说明与参数；界面上检索不到令牌原文。
- [ ] 9.4 「调用审计」视图：列表（含轮次与工具数）+ 详情抽屉，展示请求体、响应、system/user 消息、注入字符数、工具调用与状态。
  verify：页面可完整看到一次调用的请求与响应，并可直接截图用于报告。
- [ ] 9.5 对话页工具栏：有调用时在正文之前显示"本次用了哪个工具、成了没有"；无调用时不渲染该栏。
  verify：同一会话两次提问（一次命中工具、一次不命中），工具栏出现与消失均正确。

## 10. 文档与接口文档

- [ ] 10.1 `GET /openapi.yaml` 增加模板、上传、MCP、审计相关路径；`openapi_test.go` 的路由表比对通过。
  verify：`go test ./internal/httpapi/` 通过；`/swagger/` 中能看到新接口并试调。
- [ ] 10.2 新增 `backend/MCP.md` 与 `backend/AUDIT.md`，说明协议形状、令牌处理、调用循环与审计字段；同步 README 与 `deploy/README.md` 的初始化顺序（新增 `seed-apps`）。
  verify：文档与实现逐条对照无出入。

## 11. 五项验证取证

- [ ] 11.1 Q1：抓一次完整调用的请求体与响应，确认模板渲染结果在 `messages[0]`，且无模板编号字段。
  verify：`llm_calls` 该行原文 + 页面截图，两处一致。
- [ ] 11.2 Q2：对同一份材料给出 `source_chars` 与 `context_chars`，并统计资料区块字符数。
  verify：三个数字可复算，`context_chars < source_chars`。
- [ ] 11.3 Q3：对比技能文件总字数与 `system_prompt` 中「当前技能：」段字数，并给出未命中时的对照。
  verify：两次提问的 `system_prompt` 差异可解释。
- [ ] 11.4 Q4：给出一次路由的命中词与得分，并展示启用/停用技能后的两次 `system_prompt` 差异。
  verify：`skill.Score` 单测输出 + 两条真实 `system_prompt`。
- [ ] 11.5 Q5：给出含 `tools` 的那次 `request_json`，证明 `tools[*].function.parameters` 与发现结果一致；给出 `role:"tool"` 消息；给出两轮记录的 `round_no` 与 `tool_calls` 表。
  verify：`tools` 数组与 `tools/list` 逐字段对照；`SELECT round_no, tool_def_count, tool_call_count FROM llm_calls WHERE conversation_id=?`；`SELECT * FROM tool_calls WHERE conversation_id=?`。

## 12. 报告交付

- [ ] 12.1 生成 PDF 报告：项目简介、架构、五项能力说明、五项验证（含真实请求/响应片段与截图）、失败路径、已知限制。
  verify：PDF 页数与大小正常、中文可复制、截图清晰。
- [ ] 12.2 仓库公开可访问，包含全部规约与实现；报告中给出仓库地址与运行方式。
  verify：匿名访问仓库首页与 `openspec/changes/add-app-platform-mcp-and-llm-audit/` 均 200。
