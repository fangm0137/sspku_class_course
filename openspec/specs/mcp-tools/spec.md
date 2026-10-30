# mcp-tools Specification

## Purpose
接入外部工具，让模型可以调用自己的能力之外的动作。仓库内自带一个最小 MCP 服务端（单端点 JSON-RPC 2.0），教师登记并连接本班服务器后，用 tools/list 的发现结果生成工具声明并发给模型；模型只提议调用，宿主按同一份 inputSchema 校验参数、逐工具授权、带令牌执行，再把结果作为 role:tool 消息回传，可循环多轮但有轮次与次数上限。令牌以密文落库、只进不出，工具输出按不可信文本处理。

## Requirements

### Requirement: An MCP server ships with the platform and enforces bearer authentication

仓库 SHALL 在**同一个 Git 仓库**内提供 `mcp-server/`，与主应用一并由 Compose 构建启动，MUST NOT 另开第二个仓库或依赖外部托管服务。该服务 MUST 提供 `POST /mcp`（JSON-RPC 2.0，单端点）与 `GET /health`，并 MUST 至少实现 `initialize`、`tools/list`、`tools/call` 三个方法。鉴权令牌 MUST 只从环境变量读取，MUST NOT 出现在源码、镜像层、Git 历史或任何前端产物中；缺失令牌或令牌错误时 MUST 返回 401，且 MUST NOT 泄露期望值。非法 JSON、未知方法与参数缺失 MUST 分别返回可读的 JSON-RPC 错误对象（`-32700`/`-32601`/`-32602`），MUST NOT 以 200 加空结果冒充成功。

该服务 MUST 至少暴露两个工具，工具名 MUST 以小写字母开头且不含连字符，MUST 能在 `tools/list` 中被发现（每项含 `name`、`description`、`inputSchema`），MUST 能在 `tools/call` 中返回 `result.content` 文本数组与 `isError` 标记。`GET /health` MUST NOT 依赖鉴权，也 MUST NOT 返回任何工具或配置信息。

#### Scenario: Health answers without a token

- **WHEN** 不带任何令牌请求 `GET /health`
- **THEN** 返回 200 与表示存活的最简 JSON，不返回任何工具或配置信息

#### Scenario: Missing or wrong token is rejected

- **WHEN** 分别以缺少 `Authorization` 头与错误的 Bearer 令牌请求 `POST /mcp`
- **THEN** 两次都返回 401；响应体不包含期望令牌，也不包含与令牌比较有关的细节

#### Scenario: Tools are discoverable and callable

- **WHEN** 带正确令牌依次发送 `initialize`、`tools/list`、以及对其中一个工具的 `tools/call`
- **THEN** `tools/list` 返回至少两个工具，每个都含名称、说明与输入结构；`tools/call` 返回 `content` 文本数组且 `isError` 为假

#### Scenario: Malformed JSON-RPC is reported, not faked

- **WHEN** 发送非法 JSON、不存在的 `method`、以及缺少必填参数的工具调用
- **THEN** 分别返回 `-32700`、`-32601`、`-32602` 的 JSON-RPC 错误对象，且响应中不含 `result.content`

#### Scenario: The token never reaches the client or the repo

- **WHEN** 检查前端构建产物、Git 跟踪文件与接口响应
- **THEN** 三处都检索不到该令牌；仓库里只有 `.env.example` 中的空占位

### Requirement: Teachers register and connect MCP servers; tool discovery replaces the cache

平台 SHALL 允许教师登记本班 MCP 服务器，字段为名称与 Base URL；名称 MUST 在班内唯一且不超过 64 字，Base URL MUST 为 `http` 或 `https` 且不含账号密码。令牌 MUST 由教师在登记或修改时提供，MUST 只保存在服务端，MUST 以密文落库（密钥由服务端配置派生），任何接口响应 MUST NOT 回传明文令牌，只可回传"是否已配置"。**每台服务器 MUST 使用各自的令牌，MUST NOT 共用一把。**登记、连接、断开与删除 MUST 只允许教师，且 MUST 以登录用户所属班级限定，跨班或不存在一律返回与"不存在"完全一致的 404。

「连接」MUST 依次完成 `initialize` 握手与 `tools/list` 发现，并用本次结果**整体替换**旧清单（先清除再写入），随后标记为已连接；任一阶段失败 MUST 保持未连接、清空清单、保留可读的错误原因并返回 502，MUST NOT 记住一次失败的发现结果。「断开」MUST 清空该服务器的工具缓存并使已引用其工具的技能在保存与调用两处都被拒绝。连接失败 MUST NOT 阻断第 5 课已有的检索与流式回答主路径。

#### Scenario: Registering and connecting a server discovers its tools

- **WHEN** 教师登记 Base URL 指向同仓 MCP 服务的服务器并点击连接
- **THEN** 状态变为已连接；工具清单接口返回该服务实现的两个工具的名称、说明与输入结构

#### Scenario: Reconnecting replaces the cache instead of appending

- **WHEN** 对同一台服务器连续连接两次，期间远端删除其中一个工具
- **THEN** 第二次连接后本地清单等于本次发现结果，被删除的工具不残留

#### Scenario: The token is never returned

- **WHEN** 教师与学生分别读取服务器列表与详情
- **THEN** 响应只包含"是否已配置令牌"，任何字段都不等于提交的令牌原文

#### Scenario: Connection failure is explicit and isolated

- **WHEN** 教师把一个 Base URL 指向不可达地址的服务器连接，随后发起提问
- **THEN** 连接返回 502、状态为未连接且带错误摘要、清单为空；随后的提问仍返回 200 并正常作答

#### Scenario: Disconnect clears the cache

- **WHEN** 教师断开一个已连接的服务器
- **THEN** 状态变为未连接，工具清单为空；原本引用其工具的技能在保存时报 400

#### Scenario: Cross-class and non-teacher operations are refused

- **WHEN** 其他班级的账号读取、连接或删除本班服务器，以及学生调用任一 MCP 管理接口
- **THEN** 前者返回 404，后者返回 403 `forbidden`，均不产生写入

### Requirement: Skills reference MCP tools by name, validated at save time

技能的「工具」字段 SHALL 同时接受内置工具 `knowledge_search` 与 MCP 引用 `工具名@服务器名`。保存技能时 MUST 逐条校验四类条件：写法符合引用格式、服务器属于本班、服务器处于已连接状态、工具在该服务器最近一次发现结果中；任一不满足 MUST 返回 400 并指出是哪一条引用、哪一类条件失败，MUST NOT 写入半成品。配置期校验 MUST 从严（四类全查），运行期 MUST 只判"当前是否可用"，MUST NOT 把四类检查在每次提问时重做一遍。

引用的服务器在技能保存后被断开或删除时，技能行 MUST 保留（MUST NOT 级联删除），但该次调用 MUST 失败并记为 `denied`。这一时间差（保存时通过、调用时失效）MUST 在错误信息中如实呈现，MUST NOT 伪装成校验遗漏。

#### Scenario: A valid reference is accepted

- **WHEN** 教师保存一个「工具」为 `formula_hint@本班已连接的服务器` 的技能
- **THEN** 返回成功；技能详情中的工具引用与提交值一致

#### Scenario: Invalid references are rejected with the failed condition

- **WHEN** 分别提交格式错误（无 `@` 或多个 `@`）、服务器不存在、服务器属于他班、服务器未连接、工具不在清单中
- **THEN** 每次返回 400，响应指出具体引用与失败类别；技能未被创建或修改

#### Scenario: Disconnecting after binding keeps the row but blocks the call

- **WHEN** 技能保存成功后教师断开该服务器，随后提问命中该技能
- **THEN** 技能行仍在列表中；该次调用的状态为 `denied`，回答不包含该工具的输出

#### Scenario: Runtime check stays cheap

- **WHEN** 一次提问命中一个引用有效且服务器已连接的工具
- **THEN** 服务端只判断"当前可用"即发起调用，不重复执行四类配置期校验

### Requirement: Tool declarations are derived from discovery and sent with each question

服务端 SHALL 在每次提问时，把本轮候选集内的工具转换为模型可理解的工具声明（`tools` 数组），与消息一起发送给对话模型。声明的三项——`name`、`description`、`parameters`——MUST 全部来自该服务器最近一次 `tools/list` 的发现结果，MUST NOT 由人工手写或在本系统内另存一份；参数结构 MUST 被原样用作执行前的校验依据。

候选集 MUST 由本轮命中的技能决定：只有技能声明的 `工具名@服务器名` 才进入 `tools`，MUST NOT 把本班全部工具一次性交给模型。未命中任何带工具的技能时，请求体 MUST NOT 含 `tools` 字段（不是空数组）。

#### Scenario: Only the candidate subset reaches the model

- **WHEN** 本班有两台已连接服务器、共五个工具，本轮命中的技能只引用其中一个
- **THEN** 请求体的 `tools` 数组长度为 1，且其 `function.name` 等于该引用中的工具名

#### Scenario: Declarations come from discovery, not hand-written text

- **WHEN** 对比请求体里某工具的 `parameters` 与 `tools/list` 返回的 `inputSchema`
- **THEN** 两者逐字段一致（同样的属性名、类型与 `required` 列表）

#### Scenario: No tool in the round means no tools field

- **WHEN** 提问没有命中任何声明了 MCP 工具的技能
- **THEN** 请求体中不存在 `tools` 与 `tool_choice` 字段

### Requirement: The call loop is bounded and results return as tool messages

一次提问中的工具调用 MUST 是一个循环：模型返回结构化的调用请求（工具名 + 参数对象，可能一次多个）后，宿主 MUST 按声明中的输入结构校验参数，通过后才带令牌调用远端 `tools/call`；结果 MUST 以 `role: "tool"` 的消息（携带对应的 `tool_call_id`）追加进 `messages` 后再次请求模型，模型可以要求再来一轮。

循环 MUST 有终止条件：模型不再提出调用、达到轮次上限、或达到本轮调用次数上限。达到上限后 MUST 停止调用并让模型用已有信息作答，MUST NOT 报错终止整轮回答。参数校验不通过 MUST 把失败原因作为工具结果回传给模型（而不是静默跳过），使模型有机会修正。

#### Scenario: A two-step task triggers two rounds

- **WHEN** 提问需要先换算单位、再依据换算结果给出提示，且命中技能声明了两个工具
- **THEN** 该次提问内模型提出两轮调用，第二轮之后 `finish_reason` 为非工具调用，回答用到了两轮结果

#### Scenario: Parameters are validated before execution

- **WHEN** 模型返回的参数缺失必填项或类型不符
- **THEN** 服务端不发出 `tools/call`；把校验失败原因作为工具消息回传给模型，该次调用记为 `error`

#### Scenario: Round limit degrades gracefully

- **WHEN** 把轮次上限调小到 1 并提问一个需要两轮工具的问题
- **THEN** 第一轮之后停止调用，回答仍返回 200 并基于已有信息作答；不出现超限报错

#### Scenario: Failed calls are reported back to the model

- **WHEN** 远端 `tools/call` 返回 `isError` 为真或 HTTP 非 2xx
- **THEN** 该错误以工具消息回传，回答继续生成，该次调用记为 `error`

### Requirement: Untrusted tool output stays in the data position and is screened

工具返回 MUST 被当作不可信文本处理，与检索到的材料同等对待：

- **位置**：工具结果 MUST 只出现在 `role: "tool"` 消息或用户消息中，MUST NOT 拼进系统消息；系统消息中原有的边界句 MUST 保留，MUST NOT 因工具接入而削减。
- **清洗**：进入上下文之前 MUST 做一次指令式句式扫描；命中时 MUST **整条丢弃**该结果并替换为一句固定说明，MUST NOT 只删掉可疑的那一句。
- **授权**：授权 MUST 逐工具判断（"允许使用这个工具"），MUST NOT 只按服务器整体放行。
- **配额**：轮次上限与单轮调用次数上限 MUST 由宿主强制执行，与模型的判断无关。

`meta` 事件 MUST 在确有调用时携带 `tool_calls`，每项含工具引用、状态（`ok`/`denied`/`error`）、耗时与结果摘要；**本轮没有任何调用时该字段 MUST 整体省略，MUST NOT 返回空数组**。`tool_calls` MUST 先于正文送达前端；多轮时 MUST 按发生顺序逐次推送。

#### Scenario: Tool output never enters the system message

- **WHEN** 一次回答同时包含系统消息、检索资料与工具结果
- **THEN** 系统消息中检索不到工具输出的任何片段；工具输出只出现在 `role: "tool"` 消息里

#### Scenario: Instruction-like output is dropped whole

- **WHEN** 工具返回的文本包含"忽略以上指示"这类指令式语句
- **THEN** 该条结果被整条替换为固定说明，模型未读到原文；系统消息的边界句仍在

#### Scenario: Tool calls arrive before the first token

- **WHEN** 一次命中工具的提问开始流式回答
- **THEN** 前端在收到任何 `delta` 之前已收到含该调用的 `tool_calls` 信息

#### Scenario: No call means no field

- **WHEN** 提问没有触发任何工具调用
- **THEN** `meta` 中没有 `tool_calls` 字段（不是 `[]`），前端据此不渲染工具栏

#### Scenario: Denied calls are visible, never silent

- **WHEN** 技能引用的服务器已断开、属于他班，或工具名不在缓存清单中
- **THEN** `tool_calls` 中该项为 `denied`，审计中有对应记录，回答不含该工具输出
