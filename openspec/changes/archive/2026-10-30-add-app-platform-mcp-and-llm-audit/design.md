# 设计：多应用、模板、技能上传、MCP 与调用审计

## 1. 设计目标

三件事必须同时成立：

1. **五项能力都能演示**——多应用、模板、知识库、技能（内置 + 上传）、外部工具与 MCP；
2. **五问都能用运行结果回答**——不是"看代码应该会这样"，而是"这次请求的字节就是这样"；
3. **不破坏第 3–5 课已验收的行为**——班级隔离、Bearer 鉴权、未命中不伪造、SSE 事件顺序。

因此本设计的核心不是加功能，而是**让"发给模型什么"变成可查询的数据**。审计不是附属品，它是验收手段。

## 2. 关键决策

### D1 · 工具走 function calling：模型决策、宿主执行（已用网关实测确认）

第 6 课课件的要求是：「决策归模型，执行与收尾归宿主——工具声明随提问发给模型，模型输出工具名与参数，宿主按同一份结构校验、携带令牌执行，把结果作为工具消息回传，可循环多轮」。

这依赖网关支持 `tools` 与 `tool_calls`，因此**先测再定**。实测（2026-10-30，课程网关 `course-chat`）：

| 测试 | 结果 |
|---|---|
| `tools` + `tool_choice: "auto"` | 200；`finish_reason = "tool_calls"`；返回 `tool_calls[{id, type:"function", function:{name, arguments}}]` |
| 把结果以 `role:"tool"` + `tool_call_id` 回传 | `finish_reason = "stop"`，正文即最终回答（实测答「3.2公里等于3200米。」） |
| 流式 + `tools` | `delta.tool_calls` 按 `index` 增量给出：首帧含 `id` 与 `name`，后续帧追加 `arguments` 片段；结束帧 `finish_reason = "tool_calls"` |
| 顺带发现 | 流式会先推约 78 帧 `reasoning_content`（模型思考）。**必须过滤**：既不转发给浏览器，也不计入正文 |

**这条实测推翻了本设计的第一版**。第一版按「服务端预执行」写（不传 `tools`，命中技能就直接调工具），理由是「网关可能不支持 tools」。测下来网关完整支持，于是改为课件的做法。如果当时没测就落笔，第五问的答案会写成「请求体没有 tools 字段」——**那会是错的**。这正是验收要求「用运行结果回答」的意义。

采用 function calling 后的边界仍然清楚：模型只**提议**调用，它拿不到服务器地址与令牌；参数由模型填写，但 MUST 由宿主按同一份 `inputSchema` 校验后才执行。所以「模型决策」不等于「模型有权限」。

**代价（写进报告）**：请求体变长（每条工具声明都占上下文）；模型可能提出无效参数或不必要的调用——前者由校验拦住并把失败原因回传让它自我修正，后者由候选集与轮次上限约束。

### D2 · 模板只渲染一次，且渲染结果就是发送内容

`{{app_name}}`、`{{today}}`、`{{question}}` 三个占位符用一次字面替换完成。**不做递归展开**——否则提问里出现 `{{today}}` 会被二次替换，模板就变成了可被外部输入驱动的执行器。

未识别的占位符**原样保留**，不静默清空。这样「模板里写了什么」与「发出去是什么」之间的差集只有三个已知占位符，报告里可以逐字对照。

### D3 · MCP 令牌以密文落库，响应只回「是否已配置」

令牌由教师填写，服务端用 `TOKEN_SECRET` 派生的子密钥做 AES-GCM 加密后存 `VARBINARY`。响应只回 `has_token`，不回密文也不回明文。**每台服务器使用各自的令牌，不共用一把。**

> 没有选「令牌只从环境变量读」的方案：那会让教师无法在界面上完成登记，与「教师能登记本班 MCP 服务器」的要求冲突。

### D4 · 审计是旁路，写失败不影响回答

审计在模型调用**返回之后**写入。写失败只记日志，不改变回答状态码——否则「审计表坏了」会变成「整个问答不可用」，这是不可接受的反向耦合。

但**失败的调用也要记**：非 2xx、超时、连接失败都写一条 `status=failed` 的记录，否则故障时反而没有证据。

### D5 · 知识库不做全量注入

这一点是验收第 3 问的核心，必须写死：检索取前 4 条切片，每条按 800 字截断；材料全文留在 MySQL，**不进提示词**。报告里用「材料正文字符数」与「本次注入字符数」两个数字直接对比。

### D6 · 工具输出按不可信文本处理

工具返回与上传材料同等对待，四道处理：

1. **位置**——只进 `role:"tool"` 消息，绝不进系统消息；系统消息的边界句保留。
2. **清洗**——进上下文前扫一遍指令式句式，命中则**整条丢弃**并换成一句固定说明（不删单句，因为被改过的文本可能自相矛盾）。
3. **授权**——逐工具判断，不按服务器整体放行。
4. **配额**——轮次上限与单轮调用次数上限由宿主强制，与模型判断无关。

## 3. 数据模型（迁移 000006）

```sql
-- 提示词模板：班级范围，班内名称唯一
CREATE TABLE prompt_templates (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  class_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(64) NOT NULL,
  body TEXT NOT NULL,                 -- 1..4000 字，含 {{...}} 占位符
  description VARCHAR(255) NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_at/updated_at DATETIME(6),
  UNIQUE KEY uq_templates_class_name (class_id, name),
  UNIQUE KEY uq_templates_id_class (id, class_id),      -- 供应用做同班复合外键
  CONSTRAINT fk_templates_class FOREIGN KEY (class_id) REFERENCES classes(id),
  CONSTRAINT ck_templates_body CHECK (CHAR_LENGTH(body) BETWEEN 1 AND 4000)
);

-- 应用（既有 tutor_agents）加两列
ALTER TABLE tutor_agents
  ADD COLUMN prompt_template_id BIGINT UNSIGNED NULL,
  ADD COLUMN builtin_key VARCHAR(64) NULL,
  ADD UNIQUE KEY uq_agents_builtin (builtin_key),
  ADD CONSTRAINT fk_agents_template FOREIGN KEY (prompt_template_id, class_id)
      REFERENCES prompt_templates (id, class_id);

-- MCP 服务器（教师登记）
CREATE TABLE mcp_servers (
  id, class_id, name VARCHAR(64) NOT NULL,
  base_url VARCHAR(255) NOT NULL,
  auth_token_cipher VARBINARY(512) NULL,         -- AES-GCM(令牌)
  status ENUM('disconnected','connected','error') NOT NULL DEFAULT 'disconnected',
  last_error VARCHAR(255) NULL,
  connected_at DATETIME(6) NULL,
  UNIQUE KEY uq_mcp_class_name (class_id, name),
  UNIQUE KEY uq_mcp_id_class (id, class_id)
);

-- 工具清单缓存：连接时整体替换，断开时清空
CREATE TABLE mcp_tools (
  id, class_id, server_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(64) NOT NULL,
  description VARCHAR(1000) NOT NULL DEFAULT '',
  input_schema JSON NOT NULL,
  discovered_at DATETIME(6),
  UNIQUE KEY uq_mcp_tool (server_id, name),
  CONSTRAINT fk_mcp_tool_server FOREIGN KEY (server_id, class_id)
      REFERENCES mcp_servers (id, class_id) ON DELETE CASCADE
);

-- 模型调用审计：工具循环会在一次提问里产生多条
CREATE TABLE llm_calls (
  id, class_id, conversation_id NULL, user_id, agent_id NULL,
  purpose ENUM('answer') NOT NULL DEFAULT 'answer',
  round_no INT NOT NULL DEFAULT 1,               -- 同一次提问内的第几轮
  model VARCHAR(64) NOT NULL DEFAULT '',
  request_json MEDIUMTEXT NOT NULL,              -- 实际发出的请求体
  system_prompt MEDIUMTEXT NOT NULL,             -- messages 里 system 那条
  user_prompt MEDIUMTEXT NOT NULL,               -- 最后一条 user 那条
  message_count INT NOT NULL,
  context_chars INT NOT NULL,                    -- 注入的资料/工具结果字符数
  skill_name VARCHAR(32) NULL,
  tool_def_count INT NOT NULL DEFAULT 0,         -- 本次请求带了几条工具声明
  tool_call_count INT NOT NULL DEFAULT 0,        -- 模型提出了几次调用
  finish_reason VARCHAR(32) NULL,
  response_text MEDIUMTEXT NULL,
  response_json MEDIUMTEXT NULL,
  http_status INT NOT NULL,
  status ENUM('ok','failed') NOT NULL,
  error_text VARCHAR(255) NULL,
  prompt_tokens INT NULL, completion_tokens INT NULL,
  latency_ms INT NOT NULL,
  created_at DATETIME(6),
  KEY idx_llm_calls_class (class_id, created_at),
  KEY idx_llm_calls_user (user_id, created_at)
);

-- 工具调用审计：五个问题各对应一列
CREATE TABLE tool_calls (
  id, class_id, conversation_id NULL, user_id,
  llm_call_id BIGINT UNSIGNED NULL,              -- 回指产生这次调用的那一轮
  round_no INT NOT NULL,
  server_id BIGINT UNSIGNED NULL, server_name VARCHAR(64) NOT NULL,
  tool_name VARCHAR(64) NOT NULL,
  arguments_json TEXT NOT NULL,
  status ENUM('ok','denied','error') NOT NULL,
  result_excerpt VARCHAR(500) NULL,              -- 成功时的结果摘要
  error_text VARCHAR(255) NULL,                  -- 失败原因
  latency_ms INT NOT NULL,
  created_at DATETIME(6)
);
```

`llm_calls` 的 `system_prompt` / `user_prompt` / `context_chars` / `tool_def_count` 是**冗余列**，故意冗余：验收时要能用一条 `SELECT` 拿到，而不是在报告里贴一段 JSON 让人自己数。

## 4. 一次提问的完整链路（本变更后）

```
POST /api/conversations/{id}/messages
  1  鉴权 → 班级取自令牌
  2  校验提问 2..500 字
  3  读会话（属于本人、本班）→ 404 若不符
  4  读会话锁定的应用
  5  落库本轮提问；标题仍是占位串时改写
  6  取上下文窗口：末尾 6 条、每条 500 字
  7  技能路由：只在应用绑定且启用的技能里，词法计分，至多一个
  8  系统提示词：引用了模板 → 渲染；否则自由文本；都为空 → 默认角色
  9  检索：只在应用绑定材料内取前 4 条切片（每条 ≤800 字）
 10  候选集：取命中技能声明的工具引用，查缓存得到声明（name/description/inputSchema）
 11  ---- 以上完成才写响应头，此后状态码不可变 ----
 12  循环（round = 1..N）：
       12.1 发请求：messages + tools（无候选工具时不带该字段）+ tool_choice
       12.2 流式读取：累积 content → 逐帧 delta；累积 tool_calls（按 index 拼装）
            —— reasoning_content 一律丢弃
       12.3 finish_reason == tool_calls ?
              是 → 逐个调用：按 inputSchema 校验参数 → 逐工具授权
                   → 带令牌 tools/call（超时 10s）→ 清洗结果
                   → 追加 assistant(tool_calls) + role:"tool" 消息
                   → 推 tool_calls 事件（状态 + 摘要）→ round++（检查上限）
              否 → 结束循环
 13  写 llm_calls（每轮一行）与 tool_calls（每次调用一行）
 14  助手消息落库，发 done
```

第 7 步是「技能的执行逻辑」的全部答案：**没有代码执行、没有子进程、没有模型决定选哪个技能**——只有一次词法计分，然后把命中技能的 `description`（技能文件空行以下的引导规程）作为文本注入系统消息。

第 12 步是「工具的执行逻辑」：模型决定**要不要调、调哪个、参数填什么**；宿主决定**能不能调、怎么调、调几次**。两者分工由代码强制，不靠模型自觉。

## 5. 接口契约

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET/POST | `/api/prompt-templates` | 读：本班成员；写：教师 | 列表 / 新建 |
| GET/PUT/DELETE | `/api/prompt-templates/{id}` | 读：本班成员；写：教师 | 详情 / 修改 / 删除（被引用时 409） |
| POST | `/api/skills/upload` | 教师 | multipart，字段 `file`，≤64 KiB |
| GET/POST | `/api/mcp/servers` | 读：本班成员；写：教师 | 列表（含 `has_token`）/ 登记 |
| PUT/DELETE | `/api/mcp/servers/{id}` | 教师 | 修改 / 删除 |
| POST | `/api/mcp/servers/{id}/connect` | 教师 | 握手 + `tools/list` + 整体替换缓存 |
| POST | `/api/mcp/servers/{id}/disconnect` | 教师 | 清缓存 |
| GET | `/api/mcp/servers/{id}/tools` | 本班成员 | 工具清单（含 `inputSchema`） |
| GET | `/api/llm-calls` | 教师：本班；学生：自己 | 审计列表 |
| GET | `/api/llm-calls/{id}` | 同上 | 单条，含完整请求体 |
| GET | `/api/llm-calls/{id}/context-size` | 同上 | 对比接口：`source_chars` vs `context_chars` |

`POST /api/conversations/{id}/messages` 的 `meta` 事件：

```json
{"status":"ok","conversation_id":1,"skill":{"name":"数学解题引导"},
 "citations":[...],
 "tool_calls":[{"ref":"unit_convert@campus-tools","status":"ok","latency_ms":37,
                "text":"3.2 km = 3200 m"}]}
```

**`tool_calls` 只在确有调用时出现**：没有调用时该字段整体省略（不是 `[]`），前端据此不渲染工具栏。多轮时按发生顺序逐次推送。

## 6. 五项验证的证据映射

| 验证问题 | 结论要点 | 取证据的方式 |
|---|---|---|
| **Q1** 传给 LLM 的参数与返回？模板是否一并传递？ | 请求体为 `{model, messages, temperature, max_tokens, stream}`（命中工具时再加 `tools`/`tool_choice`）；模板以**渲染后全文**作为 `messages[0].content` 的一部分；模板编号与模板原文**不传** | `llm_calls.request_json`（原文）+ `system_prompt` 列；页面「调用审计 → 查看请求」截图；`SELECT system_prompt FROM llm_calls WHERE id=?` |
| **Q2** 绑定知识库是否全量传入？ | **否**。只注入命中的前 4 条切片，每条 ≤800 字 | `/api/llm-calls/{id}/context-size` 返回 `source_chars` 与 `context_chars`，两者不等；`request_json` 中资料区块可数字符 |
| **Q3** 绑定技能是否传技能全部内容？ | 只传**命中的那一个**技能的 `name` + `description`（引导规程）；`source_text` 原文**不传**；未命中时只传一句否定句 | `llm_calls.skill_name` + `system_prompt` 中「当前技能：」段落；对比技能文件总字数与系统消息内该段字数 |
| **Q4** 技能的执行逻辑？ | 服务端**词法计分路由**（不调模型）→ 注入规程 → 若声明 `knowledge_search` 则先检索；技能声明的工具则进入工具循环 | `skill.Score/Route` 单测输出 + 命中词计数；`meta.skill`；同一提问在启用/停用技能后的两次 `system_prompt` 对比 |
| **Q5** 接入 MCP 后参数有何变化？ | 请求体**新增 `tools` 数组**（每项 `{type:"function", function:{name, description, parameters}}`，`parameters` 即发现结果里的 `inputSchema`）与 `tool_choice`；候选集只含命中技能声明的工具；工具结果以 `role:"tool"` + `tool_call_id` 消息回到 `messages`；一次提问可能有多轮，每轮一条 `llm_calls` | `request_json` 中 `tools[*].function.parameters` 与 `tools/list` 的 `inputSchema` 逐字段一致；`tool_def_count`／`tool_call_count`／`round_no` 三个数字；`tool_calls` 表该轮记录 |

这张表就是报告的骨架：每个结论都指向一处**可复现的取数方式**，而不是一段说明文字。

## 7. 失败与降级矩阵

| 情形 | 行为 | 状态码/事件 |
|---|---|---|
| 模板被引用时删除 | 拒绝 | 409 `conflict` |
| 模板停用后仍被引用 | 仍渲染（停用只影响新建引用） | 200 |
| 上传技能文件超限/格式错 | 拒绝，带行号 | 400 |
| 上传的技能与 JSON 创建校验不一致 | 不允许存在 | 单元测试覆盖 |
| MCP 连接失败 | 标记 `error`，留摘要；问答主路径不受影响 | 502 `upstream_error` |
| MCP 断开后调用 | 记 `denied`，回传失败给模型，回答继续 | 200 + `tool_calls[].status=denied` |
| 工具调用超时/非 2xx/`isError` | 记 `error`，把错误作为工具消息回传给模型 | 200 + `status=error` |
| 模型给的参数不合 `inputSchema` | 不发出 `tools/call`，把校验原因回传给模型 | 200 + `status=error` |
| 达到轮次/次数上限 | 停止调用，让模型用已有信息作答 | 200，不报错 |
| 工具输出命中指令式句式 | 整条丢弃，替换为固定说明 | 200 |
| 模型网关非 2xx | 写一条 `failed` 审计；流已开始则 `error` 事件收尾 | 503 或 SSE `error` |
| 审计写入失败 | 记日志，回答不受影响 | 200 |
| 模板渲染遇未知占位符 | 原样保留 | 200 |

## 8. 迁移、回滚与兼容

- `prompt_template_id` 与 `builtin_key` 都可空：不填就是第 5 课的行为，**旧数据零迁移**。
- `mcp_*`、`llm_calls`、`tool_calls` 都是新表，删掉不影响第 3–5 课任何路径。
- 回滚口径：停用/移除四个新前端入口与对应路由即可退回第 5 课形态；数据库新表可直接丢弃。
- 内置应用与内置技能都用稳定键幂等初始化，重复执行输出 `no change`。
- `tools` 字段是**按需附加**：没有候选工具时不带，因此对第 5 课既有调用的请求体没有任何影响。

## 9. 风险

| 风险 | 处理 |
|---|---|
| 审计表膨胀（一次提问可能多行，每行含完整请求体） | 报告里说明这是**验收期设计**；生产化需要采样与 TTL，列入 Non-goals |
| 工具声明占用上下文，工具多时挤压资料 | 候选集只来自命中技能；单个技能的工具数在保存时限制上限（如 5 个） |
| 工具输出可能很长 | 单条结果截断 800 字，与资料同口径 |
| 令牌加密密钥与 JWT 共用 `TOKEN_SECRET` | 用 HKDF 风格派生独立子密钥；换 JWT 密钥会同时需要重填 MCP 令牌（写入文档） |
| 模型可能反复调用同一工具 | 轮次上限 + 单轮次数上限双约束；超限降级而非报错 |
| 内置应用被教师改坏导致演示失败 | 内置应用不可删除；重新初始化只校验不覆盖，改动需教师自行改回 |
