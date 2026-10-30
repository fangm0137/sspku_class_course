# prompt-templates Specification

## Purpose
让班级拥有一份可复用的系统提示词来源：模板属于班级、班内名称唯一、正文 1–4000 字。渲染在服务端完成且只做一次字面替换（{{app_name}} / {{today}} / {{question}}），替换进去的值不会被再次展开，未识别的占位符原样保留——这样「模板里写了什么」与「发出去是什么」之间的差集只有三个已知占位符，可以逐字核对。模板被应用引用时不可删除，停用只影响新建引用。

## Requirements

### Requirement: Prompt templates are class-scoped and reusable

平台 SHALL 提供班级范围内的提示词模板：每个模板属于一个班级，含名称、模板体、说明与启用状态。名称 MUST 在班内唯一且不超过 64 字，模板体 MUST 为 1–4000 字，MUST NOT 含 NUL 等非法字符。创建、修改、删除 MUST 只允许教师；学生 MUST 只读本班已启用的模板名称与模板体（用于理解应用会怎样被引导），MUST NOT 看到其他班级的模板。所有按编号的读取、修改与删除 MUST 以登录用户所属班级限定：不属于本班或不存在时 MUST 返回与"不存在"完全一致的 404。

#### Scenario: Teacher creates and edits a template

- **WHEN** 教师创建一个模板并在之后修改它的模板体
- **THEN** 两次都成功；班内名称冲突时返回 400，其余字段合法时模板体立即更新

#### Scenario: Validation rejects malformed templates

- **WHEN** 教师提交空模板体、超过 4000 字的模板体或含 NUL 字符的模板体
- **THEN** 返回 400 `invalid_request`，已存在的模板不被改动

#### Scenario: Cross-class access is indistinguishable from absence

- **WHEN** 其他班级的成员按编号读取、修改或删除本班模板
- **THEN** 一律返回与"不存在"完全一致的 404

#### Scenario: Students cannot modify templates

- **WHEN** 学生调用模板的创建、修改或删除接口
- **THEN** 返回 403 `forbidden`，不产生任何写入

### Requirement: Templates are rendered server-side exactly once

服务端 SHALL 在构造系统消息之前渲染模板，且 MUST 只做一次字面替换，MUST NOT 递归展开——替换进来的值里若再含占位符字面量，MUST 原样保留，不得二次替换。支持的占位符固定为三个：`{{app_name}}` 替换为应用名称、`{{today}}` 替换为服务端当天日期（`YYYY-MM-DD`，UTC）、`{{question}}` 替换为本轮提问。未识别的占位符 MUST 原样保留在输出中，MUST NOT 被静默清空、MUST NOT 因此拒绝请求。

发送给模型的 MUST 是渲染结果；模板编号与模板原文 MUST NOT 出现在请求体的任何字段中。

#### Scenario: All three placeholders are substituted

- **WHEN** 应用引用的模板体为 `你是{{app_name}}的助教。今天是{{today}}。学生问：{{question}}`
- **THEN** 系统消息首段中三处占位符都被替换为实际值；请求体里检索不到 `{{` 与 `}}`

#### Scenario: Unknown placeholders survive verbatim

- **WHEN** 模板体含 `{{class_name}}` 这类未支持的占位符
- **THEN** 系统消息中该片段原样保留为 `{{class_name}}`，请求成功

#### Scenario: Substitution is not recursive

- **WHEN** 本轮提问本身就包含字符串 `{{today}}`
- **THEN** 系统消息里出现在 `{{question}}` 位置的是提问原文 `{{today}}`，不得被再替换成日期

#### Scenario: Rendered text is what reaches the model

- **WHEN** 查看该次调用在审计中的请求体
- **THEN** 其中的 `messages[0].content` 等于渲染后的文本，且不含模板编号（如 `template_id`）字段

### Requirement: Template lifecycle protects applications that reference it

当模板仍被至少一个应用引用时，删除该模板 MUST 失败并返回 409 `conflict`，MUST NOT 级联清空引用、MUST NOT 把应用悄悄改成默认角色。停用模板 MUST 是合法操作：被停用的模板 MUST 仍可被已引用它的应用继续渲染（停用只影响"新建引用"时的可选性），此行为 MUST 与"应用被停用后已有会话仍可提问"的口径一致。

#### Scenario: Deleting a referenced template is refused

- **WHEN** 教师删除一个仍被应用引用的模板
- **THEN** 返回 409 `conflict`，模板与应用的引用关系都保持不变

#### Scenario: Deleting an unreferenced template succeeds

- **WHEN** 教师删除一个没有任何应用引用的模板
- **THEN** 返回 200，模板不再出现在列表中

#### Scenario: Disabled template still renders for existing references

- **WHEN** 教师停用了被某个应用引用的模板，随后用该应用提问
- **THEN** 请求成功，系统消息仍按该模板的渲染结果生成

#### Scenario: A disabled template cannot be newly referenced

- **WHEN** 教师尝试把已停用的模板设为某个应用当前引用的模板
- **THEN** 返回 400 `invalid_request`，应用的模板引用不变
