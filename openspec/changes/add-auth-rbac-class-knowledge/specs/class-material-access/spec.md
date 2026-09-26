# Spec Delta

## Purpose

为 CampusClaw 建立教师和学生以可信账号身份访问班级教学材料的行为合同，确保上传、文件保存、文本入库、列表刷新与材料读取始终遵守班级隔离，并定义可重复验证的本地运行及数据保留要求。

## ADDED Requirements

### Requirement: Account password authentication
系统 SHALL 允许预置教师和学生使用账号密码登录，成功返回 200 并建立有效期为 8 小时的会话；账号不存在或密码错误统一返回 401，不泄露账号存在性。所有材料列表、详情、原文件和关联知识库文本入口 MUST 验证登录身份。

#### Scenario: Teacher and students sign in
- **WHEN** 教师 A、学生 A1、学生 B1 分别提交正确账号密码
- **THEN** 各自登录成功，可使用自己的会话请求材料接口

#### Scenario: Invalid credentials
- **WHEN** 使用不存在的账号或错误密码登录
- **THEN** 均返回相同的 401 错误，不建立有效会话

#### Scenario: Anonymous or invalid session access
- **WHEN** 请求未携带会话、携带伪造会话或已过期会话，并访问列表、详情、文件或上传入口
- **THEN** 返回 401，无材料元数据、原文件或知识库文本泄露，无上传副作用

### Requirement: Server owned authorization
系统 MUST 从服务端可信账号记录获得当前角色与单一班级；请求内的班级和角色参数 SHALL 被忽略，不能改变可见范围或写入归属。上传仅允许 teacher，学生直接请求上传接口 MUST 返回 403，且在文件写入和入库前拒绝。

#### Scenario: Students call upload directly
- **WHEN** 学生 A1 或 B1 携带有效会话和合法请求保护令牌直接上传 txt/md，包括伪造 teacher 角色的请求
- **THEN** 返回 403，文件、材料及知识库记录数量均不变

#### Scenario: Teacher tampers with class
- **WHEN** 教师 A 上传合法文件并在请求中指定 B 班
- **THEN** 上传成功的材料仍属于 A 班，B 班不增加记录

#### Scenario: Student tampers with filters
- **WHEN** 学生 B1 在列表请求中指定 A 班，或学生 A1 指定 B 班
- **THEN** 仅返回调用者自身班级的数据，包括计数与元数据，不扩大权限

### Requirement: Synchronous material ingestion
系统 SHALL 接受教师上传的单个 txt/md 文件（扩展名大小写不敏感），按 UTF-8 解码并允许 BOM；Markdown 的提取结果为源码文本。上传成功 MUST 同时持久化原文件、材料记录及唯一关联的知识库文本记录，并返回 201 和材料 ID。单文件大小上限为 5 MiB。

#### Scenario: Upload txt and markdown
- **WHEN** 教师 A 分别上传带有不同标记的合法 txt 与 md 文件
- **THEN** 每次返回 201，原文件可完整取回，材料属于 A 班且关联提取文本包含各自标记，md 文本保留 Markdown 源码

#### Scenario: Invalid upload input
- **WHEN** 已授权教师上传非 txt/md、超过 5 MiB 的文件，或缺文件、多文件、空白文本、非法 UTF-8 内容
- **THEN** 分别返回 415、413、400（后四种），不创建材料或知识库记录，不保留最终文件

#### Scenario: Failed ingestion
- **WHEN** 保存文件、提取文本或写入任一数据库记录发生内部故障
- **THEN** 返回 500（数据库不可用为 503），不暴露内部路径或堆栈，列表中没有半成品，材料和知识库记录均不提交；临时或孤儿文件被补偿清理，进程中断遗留文件在重启后服务就绪前清理

### Requirement: Class isolated material reading
系统 MUST 允许同班教师和学生读取成功上传的材料列表、详情、提取文本和原文件。其他班级或不存在的材料 ID SHALL 返回相同的 404，不泄露元数据、文本或文件内容；不得存在公开静态文件或知识库入口绕过授权。

#### Scenario: Refresh after upload
- **WHEN** 教师 A 上传成功后，教师 A 与学生 A1 分别刷新列表
- **THEN** 两人均立即看到新材料，并能读取对应详情、提取文本和原文件，无需等待后台索引

#### Scenario: Cross class direct access
- **WHEN** 学生 B1 使用已知 A 班材料 ID 请求详情或原文件，或教师 A/学生 A1 使用 B 班材料 ID 请求同样入口
- **THEN** 均返回 404，不能获得文件、材料元数据或关联知识库文本

#### Scenario: No static bypass
- **WHEN** 未登录者尝试通过文件存储路径或知识库路径直接访问材料
- **THEN** 不能获取任何材料数据，应用不提供绕过认证和班级授权的读取入口

### Requirement: Credential and content safety
系统 MUST 仅持久化带随机盐的密码哈希，不将明文密码写入数据库、文件或日志；签名密钥 MUST 从环境提供，缺失或空值时启动失败，不得存在默认密钥。材料展示 SHALL 转义文本，文件 SHALL 以附件下载，文件名不能控制存储路径；Cookie 写操作 MUST 受到 CSRF 保护。

#### Scenario: Inspect stored credentials
- **WHEN** 初始化账号并完成登录后检查数据库及应用日志
- **THEN** 密码字段仅为带盐哈希，日志没有密码、完整会话凭据或密钥

#### Scenario: Missing secret
- **WHEN** 环境未提供有效的非空签名密钥
- **THEN** 应用拒绝启动并提示缺失配置，不打印密钥内容

#### Scenario: Unsafe content and filename
- **WHEN** 教师上传含 HTML 脚本样式文本或路径片段文件名的合法 txt/md
- **THEN** 文件不能写到指定存储目录之外，页面显示文本而不执行脚本，下载以附件返回

#### Scenario: Forged write request
- **WHEN** 已登录会话向上传入口发送缺失或错误 CSRF 令牌的请求
- **THEN** 请求被拒绝为 403，不能写入文件或记录

### Requirement: Local deployment health and persistence
系统 SHALL 提供 Docker Compose 本地部署方案，数据库与上传原文件 MUST 持久化在容器外的命名卷中。匿名 `GET /health` SHALL 在数据库及存储可用时返回 200，否则返回 503；响应仅包含健康状态，不泄露业务数据或配置。

#### Scenario: Anonymous health check
- **WHEN** 无会话请求健康入口，分别模拟依赖可用和不可用
- **THEN** 分别返回 200 和 503，响应没有材料、账号或密钥

#### Scenario: Recreate containers while preserving volumes
- **WHEN** 上传成功后停止并重建 Compose 容器，保留命名卷，再重新登录
- **THEN** 账号仍可登录、材料记录及关联文本仍在、原文件内容一致，班级隔离仍生效

### Requirement: Reproducible class fixtures
系统 SHALL 提供 A/B 两班及教师 A（teacher_a）、学生 A1（student_a1）、学生 B1（student_b1）的开发验收初始化方式；密码从环境输入。样本 SHALL 包含 A 班待上传 txt/md 和 B 班预置材料，采用 design.md 所列不同标记，B 班材料必须具有原文件和知识库记录。重复初始化 MUST 不重复创建数据或重置现有密码。

#### Scenario: Fixture isolation matrix
- **WHEN** 初始化完成，教师 A 上传两个 A 班样本，三个账号分别刷新列表并读取可见材料
- **THEN** 教师 A 与学生 A1 可见两个 A 班标记但不可见 B 班标记；学生 B1 仅可见 B 班标记且不能上传

#### Scenario: Initialize again
- **WHEN** 在相同数据卷上重复运行开发初始化
- **THEN** 班级、账号、B 班种子及其知识库记录不重复，已有密码哈希和上传材料不被覆盖
