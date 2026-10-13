# Spec Delta

## ADDED Requirements

### Requirement: Tutor agents are class-scoped teaching configurations

系统 SHALL 提供班级范围内的智能体配置：每个智能体属于一个班级，含名称、角色提示词与启用状态；班内名称 MUST 唯一且不超过 64 字，提示词 MUST 不超过 2000 字。提示词为空时 MUST 回退到服务端默认角色设定，且 MUST NOT 因此拒绝请求。创建、修改与启停 MUST 只允许教师；学生只能列出本班**已启用**的智能体并从中选用。所有按编号读取、修改或删除的操作 MUST 以登录用户所属班级限定：不属于本班或不存在时 MUST 返回与"不存在"完全一致的 404，MUST NOT 以 403 或 404 的差异泄露资源存在性。

#### Scenario: Teacher manages agents in the class

- **WHEN** 教师创建两个智能体并分别为它们设置名称、提示词与启用状态
- **THEN** 两个智能体都只属于该教师所在班级，班内名称唯一；修改名称或提示词后立即生效

#### Scenario: Empty prompt falls back to the default role

- **WHEN** 教师创建一个提示词为空的智能体并用它提问
- **THEN** 回答按服务端默认角色设定生成，请求成功，不因提示词为空而失败

#### Scenario: Students only see enabled agents of their own class

- **WHEN** 学生列出智能体，或按编号直接访问他班、已停用或不存在的智能体
- **THEN** 列表中只出现本班已启用的智能体；按编号访问一律返回与"不存在"完全一致的 404

#### Scenario: Non-teacher writes are rejected

- **WHEN** 学生调用创建、修改或启停智能体的接口
- **THEN** 返回 403 `forbidden`，不产生任何写入

### Requirement: Agent bindings select which skills and materials participate

系统 SHALL 以两张关联表保存智能体与技能、智能体与材料的绑定关系，MUST NOT 把绑定快照进智能体行。绑定 MUST 只接受本班技能与本班材料，跨班或不存在时 MUST 按"不存在"处理并返回 404；空绑定 MUST 是合法状态，此时该智能体没有候选技能、检索结果为空。绑定关系每次提问重新读取，教师修改绑定后 MUST 在下一次提问即刻生效，MUST NOT 要求重建智能体或新建会话。

#### Scenario: Bindings restrict the candidate set

- **WHEN** 某智能体只绑定数学技能与数学材料，用户提问语文题
- **THEN** 路由不命中任何技能，引用列表里也不出现未绑定的材料

#### Scenario: Cross-class binding is refused

- **WHEN** 教师尝试把其他班级的技能或材料绑定到本班智能体
- **THEN** 按"不存在"处理并返回 404，绑定关系不变

#### Scenario: Binding changes take effect on the next question

- **WHEN** 教师在已有会话存在的情况下修改某智能体的绑定
- **THEN** 该会话的下一次提问按新的绑定路由与检索，无需新建会话

#### Scenario: Empty bindings are a legal state

- **WHEN** 某智能体没有绑定任何技能与材料
- **THEN** 该智能体仍可被选用并作答，只是不命中技能、引用列表为空
