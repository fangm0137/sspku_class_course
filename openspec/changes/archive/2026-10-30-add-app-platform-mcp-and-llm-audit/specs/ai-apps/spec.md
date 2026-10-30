# Spec Delta

## ADDED Requirements

### Requirement: The platform ships built-in AI applications

平台 SHALL 在初始化阶段写入至少三个内置 AI 应用，覆盖不同学科场景：数学助教、语文助教、通用答疑。每个内置应用 MUST 有稳定的内置标识（`builtin_key`），初始化 MUST 以该标识幂等：首次插入，之后只校验不改写，与样板不一致时 MUST 报冲突而不是覆盖教师改过的内容。内置应用 MUST 属于某个班级，MUST 与班级内其他应用共用同一套启停、绑定与调用路径，MUST NOT 存在"内置应用走特殊代码路径"的实现。内置应用 MUST NOT 可被删除：删除请求 MUST 返回 409 `conflict`，避免验收时因误删而失去"内置应用"这一能力。教师 MUST 可以启停内置应用、修改其提示词模板与技能/材料绑定。

#### Scenario: Seeding creates three built-in applications

- **WHEN** 在已完成账号初始化的空库上执行一次内置应用初始化
- **THEN** 本班出现三个 `builtin_key` 非空的 AI 应用，分别是数学助教、语文助教与通用答疑；输出 `app seed initialized`
- **THEN** 三个应用各自绑定了与本学科对应的技能，通用答疑不绑定学科技能

#### Scenario: Re-running seeding changes nothing

- **WHEN** 串行重复执行内置应用初始化
- **THEN** 输出 `no change`；三个应用的编号、模板引用与绑定关系保持不变

#### Scenario: A teacher-edited built-in application is not overwritten

- **WHEN** 教师修改了某个内置应用的模板引用或绑定，随后再次执行初始化
- **THEN** 初始化报冲突并停止，教师改过的内容保持不变

#### Scenario: Built-in applications cannot be deleted

- **WHEN** 教师对任一内置应用发起删除
- **THEN** 返回 409 `conflict`，应用与其绑定关系保持不变
- **THEN** 非内置的应用仍可正常删除

#### Scenario: Students only see enabled applications of their own class

- **WHEN** 学生列出 AI 应用，或按编号访问其他班级、已停用或不存在的应用
- **THEN** 列表只出现本班已启用的应用；按编号访问一律返回与"不存在"完全一致的 404

### Requirement: An AI application is the composition of template, skills, materials and tools

一个 AI 应用 SHALL 由四部分组成：系统提示词来源（模板或自由文本）、候选技能集合、可检索材料范围、可调用的外部工具集合。四者 MUST 都可在不修改代码的前提下由教师调整，且 MUST 在下一次提问即刻生效，MUST NOT 要求新建会话或重建索引。当应用未引用模板时，MUST 沿用自由文本提示词；提示词与模板同时为空时，MUST 回退到服务端默认角色设定，MUST NOT 因此拒绝请求。

#### Scenario: One application answers with its own template and scope

- **WHEN** 班级内同时存在数学助教与语文助教，学生分别向两个应用的会话提同一个跨学科问题
- **THEN** 两次回答的系统提示词分别来自各自引用的模板；检索只在各自绑定的材料内进行，引用列表不出现对方应用绑定的材料

#### Scenario: Applications are independent of each other

- **WHEN** 教师修改数学助教的模板引用与材料绑定
- **THEN** 语文助教的模板、绑定与已有会话的回答不受影响

#### Scenario: Template absent falls back to free text, then to default role

- **WHEN** 应用既没有引用模板，提示词字段也为空
- **THEN** 请求成功，系统消息使用服务端默认角色设定，审计中的系统消息首段即为该默认设定

### Requirement: Application composition is inspectable before a question is asked

平台 SHALL 提供一个只读视图，列出某个应用当前实际生效的组成：模板编号与名称、候选技能名称、绑定材料数量与标题、可调用的外部工具引用。该视图 MUST 只对同一班级的成员可见，MUST NOT 返回模板以外的其他班级数据，MUST NOT 返回任何密钥。

#### Scenario: Inspecting an application shows what will participate

- **WHEN** 教师打开某个应用的组成视图
- **THEN** 返回该应用引用的模板名称、候选技能名列表、绑定材料标题列表与工具引用列表
- **THEN** 学生访问同一视图时只得到与其提问时实际生效的同一份内容

#### Scenario: Cross-class inspection is refused

- **WHEN** 其他班级的成员按编号请求本班应用的组成视图
- **THEN** 返回与"不存在"完全一致的 404
