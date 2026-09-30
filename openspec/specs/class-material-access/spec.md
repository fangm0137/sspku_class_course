# class-material-access Specification

## Purpose
为 CampusClaw 建立教师和学生以可信账号身份访问班级教学材料的行为合同，确保上传、文件保存、文本入库、列表刷新与材料读取始终遵守班级隔离，并定义可重复验证的本地运行及数据保留要求。

## Requirements

### Requirement: Explicit versioned database schema
系统 SHALL 使用 MySQL 8.0 的显式版本化 SQL 迁移创建 classes/users/materials/knowledge_entries，并由后续迁移删除已废弃的 `sessions` 表；连接和测试数据 MUST 参数化，不使用 ORM 自动建表。materials 与 knowledge_entries 的 class_id MUST 非空且具有索引。users.username 和 knowledge_entries.material_id MUST 唯一；知识库的材料与班级复合外键 MUST 阻止与来源材料不一致的班级。密码仅存 password_hash，不建立明文密码字段。迁移不插入业务种子。

#### Scenario: Repeat migration on an empty database
- **WHEN** 对空 MySQL 8.0 数据库按顺序执行全部 up，再次执行同一批 up
- **THEN** 首次建立四张业务表和迁移元数据表，第二次无变更且成功，表中无业务数据；迁移完成后 `sessions` 表不存在

#### Scenario: Database constraints reject invalid rows
- **WHEN** 测试尝试重复用户名、重复 material_id，或为材料/知识库写入 NULL class_id、无效外键、与材料不同的知识库 class_id
- **THEN** 数据库拒绝对应语句；SHOW CREATE TABLE 可查看两张表的非空列、索引和唯一/外键约束

### Requirement: Course interface contract
系统 SHALL 保留第3课速查表的路径：POST /api/login、POST /api/logout、GET /api/me、GET /api/materials、POST /api/materials、GET /api/materials/{id}、GET /api/materials/{id}/file、GET /health，并移除已废弃的 GET /api/csrf。除登录与存活检查外均 MUST 验证有效的 Bearer token；上传还 MUST 验证教师角色。登录使用 JSON username/password，上传使用 multipart 字段 file。系统 SHALL 按 design 的"接口响应约定"返回指定 JSON 字段与统一错误格式；仅下载成功返回二进制附件。错误优先级 SHALL 为 401 > 403 > 400/413/415 > 404 > 503；无法因依赖故障判定身份或对象时 MUST 直接返回 503，不伪造 401/404。当前非法文件类型仍为 400，415 为保留类别。

#### Scenario: Unified routes through Nginx
- **WHEN** 客户端通过 Nginx 请求上述路径
- **THEN** API 路径原样转发到 Go，成功读取/登录/登出返回 200，上传成功返回 201；API 错误保留约定状态码，不回退为 React HTML 页面

#### Scenario: Stable response fields
- **WHEN** 客户端登录、查询身份、上传并读取材料列表或详情
- **THEN** 登录响应含 user 对象、token 字符串与 expires_at；身份对象使用 id 而非 user_id；上传返回 material_id；列表为 items 数组，元素含 id/title/class_id/created_at；详情另含 text，空列表为 items:[]，均不泄露内部存储路径

#### Scenario: Safe error responses
- **WHEN** 任一接口产生错误，包括 Nginx 在 Go 不可达时响应 /health
- **THEN** 返回对应 HTTP 状态及仅含 error/message 的统一安全 JSON；跨班与不存在材料使用相同 not_found 消息，无材料数据、路径、凭据或字段校验细节

#### Scenario: No write-protection token endpoint
- **WHEN** 客户端请求已废弃的 GET /api/csrf
- **THEN** 返回 404，应用不再提供写请求保护令牌入口，且不因缺少该入口阻塞登录或上传

### Requirement: Server owned authorization
系统 MUST 从服务端可信账号记录获得当前角色与单一班级；token 载荷中的角色与班级仅用于签发时记录，鉴权时 MUST 以数据库账号记录为准。请求内的班级和角色参数 SHALL 被忽略，不能改变可见范围或写入归属。上传仅允许 teacher，学生直接请求上传接口 MUST 返回 403，且在文件写入和入库前拒绝。

#### Scenario: Students call upload directly
- **WHEN** 学生 A1 或 B1 携带有效 token 直接上传 txt/md，包括伪造 teacher 角色的请求体或查询参数
- **THEN** 返回 403，文件、材料及知识库记录数量均不变

#### Scenario: Teacher tampers with class
- **WHEN** 教师 A 上传合法文件并在请求中指定 B 班
- **THEN** 上传成功的材料仍属于 A 班，B 班不增加记录

#### Scenario: Student tampers with filters
- **WHEN** 学生 B1 在列表请求中指定 A 班，或学生 A1 指定 B 班
- **THEN** 仅返回调用者自身班级的数据，包括计数与元数据，不扩大权限

#### Scenario: Forged token cannot elevate
- **WHEN** 攻击者改写 token 载荷（如 role=teacher 或 classId=其它班）但无法产生正确签名，或把算法头改成 none
- **THEN** 验签失败并返回 401，不返回任何材料数据，也不能提升权限

### Requirement: Synchronous material ingestion
系统 SHALL 接受教师上传的单个 txt/md 文件（扩展名大小写不敏感），按 UTF-8 解码并允许 BOM；Markdown 的提取结果为源码文本。上传成功 MUST 同时持久化原文件、材料记录及唯一关联的知识库文本记录，并返回 201 和材料 ID。单文件大小上限为 5 MiB。

#### Scenario: Upload txt and markdown
- **WHEN** 教师 A 分别上传带有不同标记的合法 txt 与 md 文件
- **THEN** 每次返回 201，原文件可完整取回，材料属于 A 班且关联提取文本包含各自标记，md 文本保留 Markdown 源码

#### Scenario: Invalid upload input
- **WHEN** 已授权教师上传非 txt/md、超过 5 MiB 的文件，或缺文件、多文件、空白文本、非法 UTF-8 内容
- **THEN** 超限返回 413，其余情况均返回 400，不创建材料或知识库记录，不保留最终文件

#### Scenario: Multipart and filename boundaries
- **WHEN** 已授权且携带有效 token 的教师提交错误文件字段、损坏 multipart、安全展示名超过 255 码点，或总请求超过 5 MiB + 64 KiB
- **THEN** 总请求超限为 413，其余为 400，不创建文件或记录；原文件中的 BOM 保留，提取文本只移除首个 UTF-8 BOM

#### Scenario: Failed ingestion
- **WHEN** 保存文件、提取文本或写入任一数据库记录发生确定的失败，事务尚未提交
- **THEN** 返回 500（数据库不可用为 503），不暴露内部路径或堆栈，列表中没有半成品，材料和知识库记录均不提交；执行一次文件补偿，遗留临时或孤儿文件在业务就绪前清理。持续清理失败保持业务未就绪并等待人工处理，不实现自动重试策略；health 不等待清理

#### Scenario: Commit result unknown after response interruption
- **WHEN** 事务已提交但响应中断，客户端未收到 201
- **THEN** 原文件、材料与知识库记录已持久化，服务端可查到；客户端重试视为独立上传，本次迭代不提供幂等键

#### Scenario: Database commit acknowledgement lost
- **WHEN** 提交期间数据库连接中断，服务端无法确认提交结果
- **THEN** 服务端保留文件并在仍可响应时返回 503；数据库恢复后按材料 ID 核实提交结果再决定是否清理，不能删除已提交记录引用的文件

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

#### Scenario: Invalid material identifier and missing content
- **WHEN** 已登录用户请求非规范正整数材料 ID，或其本班材料存在但关联正文/原文件缺失或不可读
- **THEN** 非规范 ID 与不存在材料同形返回 404；已授权但内容损坏返回安全 500，不泄露路径；未登录仍优先返回 401

### Requirement: Reproducible class fixtures
系统 SHALL 提供 A/B 两班及教师 A（teacher_a）、学生 A1（student_a1）、学生 B1（student_b1）的开发验收初始化方式；密码从环境输入。样本 SHALL 包含 A 班待上传 txt/md 和 B 班预置材料，采用 design.md 所列不同标记，B 班材料必须具有原文件和知识库记录。重复初始化 MUST 不重复创建数据或重置现有密码。

材料 SHALL 使用 source=seed/upload 标记来源，B 班种子为 seed，教师上传为 upload。此处重复初始化指正常串行执行；并发初始化与种子执行中途失败补偿不在本迭代范围。

#### Scenario: Fixture isolation matrix
- **WHEN** 初始化完成，教师 A 上传两个 A 班样本，三个账号分别刷新列表并读取可见材料
- **THEN** 教师 A 与学生 A1 可见两个 A 班标记但不可见 B 班标记；学生 B1 仅可见 B 班标记且不能上传

#### Scenario: Initialize again
- **WHEN** 在相同数据卷上重复运行开发初始化
- **THEN** 班级、账号、B 班种子及其知识库记录不重复，已有密码哈希和上传材料不被覆盖

### Requirement: JWT bearer authentication
系统 SHALL 允许预置教师和学生使用账号密码登录，成功返回 200 并签发有效期 8 小时的 JWT；账号不存在或密码错误统一返回 401，不泄露账号存在性。所有材料列表、详情、原文件和关联知识库文本入口 MUST 验证登录身份。

系统 MUST 使用 HS256 签名的无状态 token 承载身份，签名密钥从环境提供的 TOKEN_SECRET 读取。登录响应 SHALL 在 body 中返回 token 及其到期时间，MUST NOT 设置会话 Cookie。每个受保护请求 MUST 从 `Authorization: Bearer <token>` 读取 token，校验签名、算法与有效期后，再用 token 中的账号 ID 回查可信账号记录取得角色与班级；账号记录查询所需数据库不可用 MUST 返回 503，不得误返回 401。登出 SHALL 由客户端丢弃 token 完成，服务端不保存可被重放的会话状态；`POST /api/logout` 在 token 有效时返回 200。

#### Scenario: Teacher and students sign in
- **WHEN** 教师 A、学生 A1、学生 B1 分别提交正确账号密码
- **THEN** 各自登录成功并在响应体中获得可用 token，携带该 token 可请求本班材料接口；响应不设置会话 Cookie

#### Scenario: Invalid credentials
- **WHEN** 使用不存在的账号或错误密码登录
- **THEN** 均返回相同的 401 错误，不签发 token

#### Scenario: Malformed login input
- **WHEN** 提交缺失字段、null、空串、超长、类型错误或非法 JSON 的登录请求；username 合法长度为 1–64 个 Unicode 码点且不能全空白，password 为 1–128 个 Unicode 码点
- **THEN** 返回 400 invalid_request 且不签发 token，统一安全消息不泄露字段校验细节

#### Scenario: Login request envelope
- **WHEN** 登录请求使用非 application/json、超过 16 KiB 的请求体、重复字段名、尾随多个 JSON 值或非法 UTF-8
- **THEN** 返回同形 400 invalid_request，不签发 token；多余的角色与班级字段被忽略，身份只能来自数据库账号

#### Scenario: Anonymous or invalid token access
- **WHEN** 请求缺失 `Authorization` 头、使用非 `Bearer` 方案、或携带签名错误、算法不符、结构损坏及已过期的 token，并访问列表、详情、文件或上传入口
- **THEN** 返回 401，无材料元数据、原文件或知识库文本泄露，无上传副作用

#### Scenario: Browser session recovery and service failure
- **WHEN** 页面刷新后用 localStorage 中的 token 调用 GET /api/me 恢复身份，或已登录用户刷新材料、退出时收到 503；请求恢复后由用户重试
- **THEN** 已知身份在 503 时保持，首次加载无法确认身份时显示可重试状态，不误显示已退出；仅明确的受保护请求 401 清除身份、材料与已保存 token

#### Scenario: Restore current identity
- **WHEN** 已登录用户刷新前端并携带 token 调用 GET /api/me
- **THEN** 返回 200 和服务端当前 id、username、role、class_id，其中 id 为用户 ID；不返回密码，也不回显 token 原文

#### Scenario: Logout discards token
- **WHEN** 已登录客户端调用 POST /api/logout 并丢弃本地 token，随后再用该 token 请求材料接口
- **THEN** 登出在 token 有效时返回 200，服务端不产生可被删除的会话记录；客户端进入未登录状态并清空材料

#### Scenario: Identity database unavailable
- **WHEN** 携带签名有效的 token 请求 /api/me 或材料接口时，读取账号记录所需数据库不可用
- **THEN** 返回 503 且无材料泄露，前端展示暂不可用，不按 401 清空身份

### Requirement: Credential and token safety
系统 MUST 仅持久化带随机盐的密码哈希，不将明文密码写入数据库、文件或日志；token 签名密钥 TOKEN_SECRET MUST 从环境提供，缺失或空值时启动失败，不得存在默认密钥。服务端 MUST NOT 将 token 原文、密码或签名密钥写入日志，也 MUST NOT 持久化可用于重放的会话记录；token 签名的校验 MUST 使用恒定时间比较。材料展示 SHALL 转义文本，文件 SHALL 以附件下载，文件名不能控制存储路径。

#### Scenario: Inspect stored credentials
- **WHEN** 初始化账号并完成登录后检查数据库及应用日志
- **THEN** 密码字段仅为带盐哈希，日志没有密码、token 原文或签名密钥

#### Scenario: Missing secret
- **WHEN** 环境未提供有效的非空 TOKEN_SECRET 签名密钥
- **THEN** 应用拒绝启动并提示缺失配置，不打印密钥内容

#### Scenario: No server-side replayable session
- **WHEN** 登录成功后查询数据库
- **THEN** 不存在保存该 token 或会话标识的服务端表，`sessions` 表已被迁移删除

#### Scenario: Client token storage and restart
- **WHEN** 前端登录成功后检查浏览器存储，随后刷新页面
- **THEN** token 保存在 localStorage，刷新后仍可携带它恢复登录态；服务端不设置会话 Cookie，浏览器不持有身份 Cookie

#### Scenario: Unsafe content and filename
- **WHEN** 教师上传含 HTML 脚本样式文本或路径片段文件名的合法 txt/md
- **THEN** 文件不能写到指定存储目录之外，页面显示文本而不执行脚本，下载以附件返回

### Requirement: Local deployment health and token persistence
系统 SHALL 采用 Go 后端、MySQL 8.0、React 18 TypeScript Vite 前端及 Nginx 入口，提供 Docker Compose 本地部署方案；数据库与上传原文件 MUST 持久化在容器外的命名卷中。匿名 `GET /health` SHALL 只表示进程存活，Go 可响应时返回 200 与 {"status":"ok"}，MUST NOT 查询数据库、验证 token 或检查存储。依赖数据库的业务接口在数据库不可用时 MUST 返回 503，不泄露业务数据或配置。

#### Scenario: Anonymous health check
- **WHEN** 无 token 请求健康入口，分别模拟数据库/存储可用和不可用，Go 进程仍可响应
- **THEN** 均返回 200 与 {"status":"ok"}，不访问数据库或存储，响应没有材料、账号或密钥

#### Scenario: Business dependency outage and recovery
- **WHEN** 已登录后数据库停止，再请求 /health 与材料接口，随后恢复数据库
- **THEN** 故障期间 /health 为 200、材料接口为 503；恢复后未过期 token 可再次读取本班材料，业务恢复不依赖重新签发 token

#### Scenario: API process unavailable
- **WHEN** Go 进程停止，匿名客户端经 Nginx 请求 /health
- **THEN** 入口返回 503 与 {"error":"service_unavailable","message":"服务暂不可用"}，不得返回静态成功响应

#### Scenario: Recreate containers while preserving volumes
- **WHEN** 上传成功后停止并重建 Compose 容器，保留命名卷，再重新登录
- **THEN** 账号仍可登录、材料记录及关联文本仍在、原文件内容一致，班级隔离仍生效

#### Scenario: Token survives container recreation
- **WHEN** 未过期 token 存在时重建容器，保留数据卷并使用相同 TOKEN_SECRET，重建完成时 token 仍未到期
- **THEN** 原 token 仍可用于访问本班材料，无需重新登录；过期 token 不会因重建而延长有效期
