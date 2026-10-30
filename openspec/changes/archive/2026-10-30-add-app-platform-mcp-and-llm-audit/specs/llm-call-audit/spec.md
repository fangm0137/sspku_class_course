# Spec Delta

## ADDED Requirements

### Requirement: Every model call is recorded verbatim

服务端 SHALL 为每一次对模型网关的调用写入一条审计记录，记录 MUST 包含：调用目的、**轮次序号**（同一次提问内的第几轮，工具循环会在一轮提问里产生多条）、模型名、**完整的请求体原文**、响应正文、HTTP 状态、调用结果（成功/失败）、耗时与网关返回的 token 用量。请求体原文 MUST 与真正发出的字节一致，MUST NOT 事后重建或摘要化——否则"发给模型什么"就无法作为证据。

审计 MUST 同时保存三个派生字段以便直接核对而不必解析 JSON：系统消息全文、最后一条用户消息全文、注入的资料字符数。这三项 MUST 由实际发送的消息计算得出，MUST NOT 由调用方另行传入。

审计写入 MUST 是尽力而为的旁路：写入失败 MUST NOT 让本次回答失败或改变其状态码，但 MUST 在服务端日志留下可检索的记录。

#### Scenario: One question produces one audit row

- **WHEN** 学生在一个已绑定材料与技能的应用里完成一次提问
- **THEN** 审计表新增一条记录，其请求体可解析出 `model`、`messages`、`temperature`、`max_tokens`、`stream` 等字段
- **THEN** 记录的 `status` 为成功，`http_status` 为 2xx，耗时与 token 用量非空

#### Scenario: The stored request equals what was sent

- **WHEN** 把审计中的请求体与网关侧观察到的请求逐字段比对
- **THEN** 两者一致；请求体中 `messages` 的条数等于"系统消息 1 条 + 历史消息 n 条 + 本轮用户消息 1 条"

#### Scenario: Audit failure does not break the answer

- **WHEN** 审计写入失败（例如数据库该表不可写）而模型调用本身正常
- **THEN** 本轮回答仍返回 200 且内容完整；日志中出现一条审计失败的记录

#### Scenario: Failed model calls are recorded too

- **WHEN** 模型网关返回非 2xx 或连接失败
- **THEN** 仍写入一条 `status` 为失败的记录，含 HTTP 状态或错误摘要，且不含密钥

### Requirement: The audit answers what was actually sent, without reading code

审计记录 MUST 足以直接回答下列五问，且 MUST 能通过接口按编号取回完整请求体：

1. 本次调用的完整参数与返回内容是什么；
2. 提示词模板的内容是否随请求传递（以何种形态）；
3. 绑定知识库后是否把知识库的全部内容传入；
4. 绑定技能后技能内容传入了多少；
5. 接入外部工具后请求参数有何变化（`tools` 数组是否存在、候选集如何收窄、工具结果以何种角色回到消息序列）。

为此，记录 MUST 至少可取出：系统消息全文、用户消息全文、消息总条数、注入资料字符数、本轮命中的技能名、本轮的工具调用条数。平台 SHOULD 提供一个对比接口，返回"材料全文总字符数"与"本次实际注入字符数"，使第 3 问可以用两个数字直接回答。

#### Scenario: Answering question two from the record

- **WHEN** 取回一次调用了模板的请求记录
- **THEN** 系统消息全文以渲染后的模板开头，可逐字核对；请求体中不含模板编号字段

#### Scenario: Answering question three with two numbers

- **WHEN** 对一份已入库材料提问并查询对比接口
- **THEN** 返回该材料的正文字符总数与本次注入的字符数；两者不相等，注入量受切片条数与单条截断上限约束

#### Scenario: Answering question four from the record

- **WHEN** 提问命中了某个技能
- **THEN** 记录中技能名非空，系统消息里可定位到该技能的引导规程段落；未命中时技能名为空且系统消息含"没有匹配到已启用的学科技能"一句

#### Scenario: Answering question five from the record

- **WHEN** 提问命中了声明外部工具的技能
- **THEN** 该次记录关联至少一条工具调用审计；请求体含 `tools` 数组，其 `function.name` 等于命中技能声明的工具名，并带 `tool_choice`
- **THEN** 消息序列里可定位到 `role: "tool"` 的消息与它对应的 `tool_call_id`，且该轮记录的 `round` 大于 1

#### Scenario: No tool means no tools field

- **WHEN** 提问没有命中任何声明了外部工具的技能
- **THEN** 该次请求体中不存在 `tools` 字段，`tool_call_count` 为 0

### Requirement: Tool calls are recorded with their own five answers

平台 SHALL 为每一次工具调用写入一条独立记录，MUST 能回答五个问题：调了什么（服务器名 + 工具名）、成了没有（`ok` / `denied` / `error`，成功记结果摘要、失败记原因）、谁触发的（用户）、发生在哪次对话与第几轮、什么时候（时间戳）。记录 MUST NOT 写入令牌原文或请求头。

#### Scenario: A successful call leaves a complete record

- **WHEN** 一次工具调用成功
- **THEN** 记录含服务器名、工具名、状态 `ok`、结果摘要、用户编号、会话编号、轮次与时间

#### Scenario: Denied and failed calls are distinguishable

- **WHEN** 分别制造一次越权（服务器已断开）与一次远端错误
- **THEN** 两条记录的状态分别为 `denied` 与 `error`，且 `error` 那条带可读原因

#### Scenario: Token never appears in a tool record

- **WHEN** 检索工具调用记录的所有字段
- **THEN** 找不到该服务器的令牌原文，也找不到 `Authorization` 头内容

### Requirement: Audit is class-scoped and carries no secrets

审计 MUST 按班级隔离：教师 MUST 只能读取本班记录，学生 MUST 只能读取自己发起的记录；其他班级或越权访问 MUST 返回与"不存在"完全一致的 404。审计记录 MUST NOT 包含任何凭据：MUST NOT 保存请求头（其中可能含 `Authorization`）、MUST NOT 保存网关 API Key、MUST NOT 保存工具的 Bearer 令牌。响应 MUST 只返回记录本身，MUST NOT 附带其他班级的会话或材料信息。

#### Scenario: Teachers and students see different scopes

- **WHEN** 教师与学生分别读取审计列表
- **THEN** 教师看到本班全部记录；学生只看到自己发起的记录；两者都看不到其他班级的记录

#### Scenario: No credential is stored or returned

- **WHEN** 检索审计记录与接口响应中的敏感串
- **THEN** 找不到网关 API Key、工具 Bearer 令牌或 `Authorization` 头的内容；请求体中原有的敏感字段（若有）在写入前被剔除

#### Scenario: Cross-class audit access is refused

- **WHEN** 其他班级的账号按编号读取本班的一条审计记录
- **THEN** 返回与"不存在"完全一致的 404
