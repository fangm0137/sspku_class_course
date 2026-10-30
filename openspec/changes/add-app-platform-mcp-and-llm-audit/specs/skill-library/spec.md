# Spec Delta

## ADDED Requirements

### Requirement: Built-in skills ship with the platform and are identifiable

平台 SHALL 在初始化阶段写入至少两份内置技能（数学解题引导、语文解题引导），每份 MUST 带稳定的内置标识，初始化 MUST 幂等。列表接口 MUST 区分内置与自建两类（内置返回 `builtin: true`），使"平台自带技能"与"教师上传技能"在界面上可分辨。内置技能 MUST NOT 可被删除：删除请求 MUST 返回 409 `conflict`，但 MUST 可被停用——停用后 MUST 不再参与路由，且行为变化 MUST 可在回答中观察到。

#### Scenario: Built-in skills are listed and marked

- **WHEN** 教师列出本班技能
- **THEN** 至少两条记录的 `builtin` 为真，名称分别为数学解题引导与语文解题引导，且各自带有「适用条件」与引导规程

#### Scenario: Seeding is idempotent

- **WHEN** 串行重复执行技能初始化
- **THEN** 输出 `no change`，记录编号与内容保持不变

#### Scenario: Built-in skills cannot be deleted but can be disabled

- **WHEN** 教师删除内置技能，随后改为停用该技能并再次提问
- **THEN** 删除返回 409 `conflict` 且记录仍在；停用后该技能不再被路由命中，回答中不再出现它的引导规程

#### Scenario: Disabling a guiding skill changes the answer

- **WHEN** 对一个原本会命中「解题引导」类技能的提问，分别在启用与停用该技能后各问一次
- **THEN** 启用时系统消息含"不要写出最终答案、选项、得数"的硬性要求；停用后该要求消失，两次回答的措辞可观察到差异

### Requirement: Skills can be uploaded as files

平台 SHALL 提供技能上传入口，接受 `multipart/form-data` 且只允许一个名为 `file` 的文件。文件 MUST 为 UTF-8 文本，扩展名 MUST 为 `.txt` 或 `.md`，大小 MUST 不超过 64 KiB。上传 MUST 与既有的 JSON 创建共用同一套解析与校验（元数据键行、引导规程、工具白名单、各字段长度），MUST NOT 存在"上传路径跳过某条校验"的分支。解析失败 MUST 返回 400 并携带行号与字段名。上传成功 MUST 返回 201 与新技能编号，且该技能 MUST 立即可被应用绑定并在下一次提问参与路由。

#### Scenario: A valid file is accepted

- **WHEN** 教师上传一份合法的 `.md` 技能文件
- **THEN** 返回 201 与技能编号；列表中出现该技能，`builtin` 为假；把它绑定到某个应用后提问，路由可命中

#### Scenario: Rejected inputs are explained

- **WHEN** 教师上传 `.pdf` 文件、超过 64 KiB 的文件、或元数据缺字段的文件
- **THEN** 分别返回 400，响应体说明被拒绝的原因；缺字段时给出行号与字段名
- **THEN** 被拒绝的上传不产生任何技能记录

#### Scenario: Upload and JSON creation enforce the same rules

- **WHEN** 用同一份非法原文分别走上传与 JSON 创建
- **THEN** 两次都返回 400，且错误文案指向同一处问题（同一行、同一字段）

#### Scenario: Non-teacher uploads are refused

- **WHEN** 学生调用技能上传接口
- **THEN** 返回 403 `forbidden`，不产生任何写入

#### Scenario: Uploads are class-scoped

- **WHEN** 教师上传技能后用其他班级的账号列出技能
- **THEN** 列表中没有这条记录；按编号访问返回 404
