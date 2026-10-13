# Spec Delta

## ADDED Requirements

### Requirement: Conversations are private, class-scoped and bound to one agent

系统 SHALL 用 `conversations` 记录一次连续追问的归属与角色：所属班级取自登录用户（请求无法指定）、归属用户、**新建时选定的智能体**与标题。新建会话 MUST 校验所选智能体属于本班且处于启用状态，不满足时 MUST 返回与"不存在"完全一致的 404；本班当时没有任何已启用智能体时 MUST 返回 400。会话的 `agent_id` MUST 在新建时写入且此后不再变更，请求中携带的其他智能体编号 MUST NOT 改变它；要换智能体 MUST 新开会话。智能体后来被停用 MUST NOT 影响已有会话继续提问。标题 MUST 在新建时写入占位串，首轮提问到达后由服务端改写成该提问的前 24 字（结果为空则保留占位串）；此后每轮 MUST 刷新更新时间，会话列表按更新时间倒序。

#### Scenario: New conversation binds an enabled agent of the class

- **WHEN** 用户新建会话并选定本班一个已启用的智能体
- **THEN** 会话写入该智能体编号，标题为占位串，此后每轮回答都按该智能体的提示词与绑定

#### Scenario: No enabled agent in the class

- **WHEN** 本班没有任何已启用的智能体，用户尝试新建会话
- **THEN** 返回 400，提示本班暂无可用的智能体

#### Scenario: The agent is locked after creation

- **WHEN** 会话已选定智能体 A，之后的消息体里再携带智能体 B 的编号
- **THEN** 回答仍按 A 的提示词与绑定生成，编号 B 被丢弃

#### Scenario: Disabling an agent does not break existing conversations

- **WHEN** 会话选定的智能体后来被停用，用户用该会话继续提问
- **THEN** 提问正常受理；停用只影响新建会话时的选用

#### Scenario: Others' and other classes' conversations are indistinguishable from missing

- **WHEN** 用户按编号读取他人或他班的会话
- **THEN** 返回 404，且响应与读取一个不存在编号时完全一致；未登录访问返回 401

#### Scenario: Title is rewritten by the server

- **WHEN** 首轮提问到达一个标题仍为占位串的会话
- **THEN** 标题被改写为该提问的前 24 字（为空则保留占位串），更新时间随之刷新

### Requirement: Messages persist the exchange and the context window is bounded

系统 SHALL 用 `messages` 按写入顺序保存每一句：角色只有 `user` 与 `assistant`（由数据库约束），正文为 `content`，助手消息额外保存本次的引用列表与命中的技能名，用户消息不含这两项。上下文 MUST 使用固定滑动窗口：每次回答都重新查询该会话的消息，按写入顺序取**末尾 6 条**，每条正文截断到 **500 字**；角色不是 `user` 或 `assistant`、以及正文为空的记录 MUST 被跳过。本轮提问 MUST 在生成之前写入库，因而必定落在窗口内；更早的消息 MUST 仍可读取，只是不进入上下文。

#### Scenario: Messages are stored in order

- **WHEN** 一轮提问完成
- **THEN** 用户消息与助手消息按顺序各写入一条，助手消息带上引用列表与技能名，用户消息不带

#### Scenario: Only the last six messages enter the context

- **WHEN** 一个会话累计超过 6 条消息后继续提问
- **THEN** 送入上下文的是末尾 6 条，更早的消息不再进入，但仍可在会话读取接口中看到

#### Scenario: Each message is truncated

- **WHEN** 窗口内某条消息正文超过 500 字
- **THEN** 送入上下文时被截断到 500 字，库中的原文不变

#### Scenario: The current question is inside the window

- **WHEN** 处理本轮提问
- **THEN** 该提问已先写入库，因此在同一窗口内可见

#### Scenario: Illegal roles and empty content are skipped

- **WHEN** 会话中存在角色非 `user`/`assistant` 或正文为空的消息
- **THEN** 这些记录被跳过，不进入上下文
