# Spec Delta

## ADDED Requirements

### Requirement: Skill files are parsed and validated on submission

系统 SHALL 接受教师提交的**单个文本文件**作为技能定义：空行以上为元数据区，空行以下为引导规程。元数据 MUST 支持名称、适用条件、资料、工具、启用五项中文键行，`#` 开头的行与空行在元数据区 MUST 被忽略。校验规则：文件 MUST 不超过 8000 字；名称、适用条件与启用三项 MUST 必填；名称 MUST 不超过 32 字、适用条件 MUST 不超过 120 字、说明 MUST 不超过 2000 字、资料 MUST 不超过 32 字；同一字段重复出现、出现未定义字段、或空行以下缺少正文时 MUST 拒绝提交；工具 MUST 只接受 `knowledge_search`，其他取值 MUST 在解析阶段即被拒绝。拒绝时必须返回面向教师的可读错误信息。解析成功时 MUST 保留文件原文以便取回再编辑，且技能行上的每一列都能溯源到文件字段。

#### Scenario: A valid file becomes a skill row

- **WHEN** 教师提交一份包含名称、适用条件、资料、工具、启用与引导规程的文件
- **THEN** 该技能写入本班技能表，名称、适用条件、资料、工具、启用与说明逐项对应文件内容，原文一并保留

#### Scenario: Missing required fields are rejected with a readable reason

- **WHEN** 提交的文件缺少名称、适用条件或启用中的任意一项
- **THEN** 提交被拒绝，返回可读的字段级错误信息，不产生技能行

#### Scenario: Duplicate and undefined fields are rejected

- **WHEN** 同一字段在元数据区出现两次，或出现未定义的字段名
- **THEN** 提交被拒绝，错误信息指出具体字段

#### Scenario: Illegal tool name is rejected at parse time

- **WHEN** 工具字段的值不是 `knowledge_search`
- **THEN** 提交被拒绝，不产生技能行

#### Scenario: Missing body or oversized file is rejected

- **WHEN** 空行以下没有正文，或文件超过 8000 字
- **THEN** 提交被拒绝，不产生技能行

### Requirement: Skills are managed per class by teachers

系统 SHALL 提供技能的增删改查：读取允许本班登录用户，写入（创建、修改、删除）MUST 只允许教师。名称在班内 MUST 唯一，同名提交 MUST 返回 400。按编号访问他班或不存在的技能 MUST 返回与"不存在"完全一致的 404。技能被停用后 MUST 不再参与路由，即使它仍被某个智能体绑定。

#### Scenario: Duplicate name in the same class is rejected

- **WHEN** 教师提交一个与班内已有技能同名的技能
- **THEN** 返回 400，原技能不变

#### Scenario: Students can read but not write

- **WHEN** 学生列出本班技能，然后尝试创建、修改或删除技能
- **THEN** 列表正常返回本班技能；写操作返回 403 `forbidden`

#### Scenario: Cross-class skill is invisible

- **WHEN** 教师按编号读取或修改其他班级的技能
- **THEN** 返回与"不存在"完全一致的 404

#### Scenario: Disabled skill stops participating

- **WHEN** 把某个仍被智能体绑定的技能改为停用后再提同一句问
- **THEN** 该技能不再进入候选集，路由结果改变（或不再命中技能），无需新建会话

### Requirement: Skill routing scores trigger text lexically

系统 SHALL 在**该智能体绑定且已启用**的候选集内做确定性词法计分，MUST NOT 调用模型、MUST NOT 做语义匹配。计分规则：把适用条件按中英文逗号、顿号、分号或竖线切分为关键词，逐个判断是否为提问的子串，命中一个词得一分；两侧 MUST 先统一转为小写；取最高分的一条，同分时 MUST 取编号较小的一条；零命中即不选技能。一次回答 MUST 至多注入一条技能，未绑定或已停用的技能即使适用条件命中 MUST NOT 参与。

#### Scenario: Highest score wins

- **WHEN** 提问同时命中两份已绑定技能的适用条件，其中一份命中更多关键词
- **THEN** 只有得分最高的那一条被选中，系统消息里只出现一份引导正文

#### Scenario: Tie breaks on the smaller id

- **WHEN** 两份候选技能得分相同
- **THEN** 选中编号较小的那一条

#### Scenario: Zero matches selects no skill

- **WHEN** 提问与候选集内所有技能的适用条件都不匹配
- **THEN** 不选技能，回答直接按智能体提示词生成

#### Scenario: Unbound or disabled skills never enter the candidate set

- **WHEN** 某技能的适用条件命中了提问，但它未被该智能体绑定、或已被停用
- **THEN** 该技能不参与计分，也不出现在候选集里

#### Scenario: Case folding applies to both sides

- **WHEN** 适用条件写作 `Equation`，提问中写作 `equation`
- **THEN** 仍然判定为命中
