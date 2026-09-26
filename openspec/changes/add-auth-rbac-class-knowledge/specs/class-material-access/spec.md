# Spec Delta

## Purpose

为 CampusClaw 建立教师和学生以可信账号身份访问班级教学材料的行为合同，确保上传、文件保存、文本入库、列表刷新与材料读取始终遵守班级隔离，并定义可重复验证的本地运行及数据保留要求。

## ADDED Requirements

### Requirement: Account password authentication
系统 SHALL 允许预置教师和学生使用账号密码登录，成功返回 200 并建立有效期为 8 小时的会话；账号不存在或密码错误统一返回 401，不泄露账号存在性。所有材料列表、详情、原文件和关联知识库文本入口 MUST 验证登录身份。

系统 MUST 使用 MySQL 服务端会话表和不透明 Cookie ID，Cookie 不携带可信用户/角色/班级载荷；每次请求通过有效会话关联账号记录取得身份。登录成功 SHALL 换发不可预测 ID 并使本次请求的旧会话失效。登出 SHALL 删除服务端会话并清除 Cookie，查询或删除会话时数据库不可用 MUST 返回 503，不得误返回 401 或宣称登出成功。

#### Scenario: Teacher and students sign in
- **WHEN** 教师 A、学生 A1、学生 B1 分别提交正确账号密码
- **THEN** 各自登录成功，可使用自己的会话请求材料接口

#### Scenario: Invalid credentials
- **WHEN** 使用不存在的账号或错误密码登录
- **THEN** 均返回相同的 401 错误，不建立有效会话

#### Scenario: Malformed login input
- **WHEN** 携带有效匿名 CSRF 令牌提交缺失字段、null、空串、超长、类型错误或非法 JSON 的登录请求；username 合法长度为 1–64 个 Unicode 码点且不能全空白，password 为 1–128 个 Unicode 码点
- **THEN** 返回 400 invalid_request 且不建立已登录会话，统一安全消息不泄露字段校验细节

#### Scenario: Anonymous or invalid session access
- **WHEN** 请求未携带会话、携带伪造会话或已过期会话，并访问列表、详情、文件或上传入口
- **THEN** 返回 401，无材料元数据、原文件或知识库文本泄露，无上传副作用

#### Scenario: Login replaces old session
- **WHEN** 携带已有会话再次成功登录，然后用旧 Cookie 请求材料
- **THEN** 新会话有效，旧 Cookie 返回 401；Cookie 中不包含用户、角色或班级字段

#### Scenario: Logout invalidates replay
- **WHEN** 有效会话携带合法请求保护令牌调用 POST /api/logout，再使用事先保存的旧 Cookie 请求材料
- **THEN** 登出返回 200、服务端会话被删除且浏览器 Cookie 被清除，旧 Cookie 重放返回 401

#### Scenario: Restore current identity
- **WHEN** 已登录用户刷新前端并调用 GET /api/me
- **THEN** 返回 200 和服务端当前 id、username、role、class_id，其中 id 为用户 ID；不返回密码、会话 ID 或会话摘要

#### Scenario: Session database unavailable
- **WHEN** 携带会话 ID 请求 /api/me、/api/logout 或材料接口时，会话查询所需数据库不可用
- **THEN** 返回 503 且无材料泄露，前端展示暂不可用，不按 401 清空身份或报告登出成功

### Requirement: Course interface contract
系统 SHALL 保留第3课速查表的路径：POST /api/login、POST /api/logout、GET /api/me、GET /api/materials、POST /api/materials、GET /api/materials/{id}、GET /api/materials/{id}/file、GET /health，并增加 GET /api/csrf。除登录、CSRF 令牌获取与存活检查外均 MUST 验证有效登录会话；上传还 MUST 验证教师角色，登录、登出及上传 MUST 校验 CSRF。登录使用 JSON username/password，上传使用 multipart 字段 file。系统 SHALL 按 design 的“接口响应约定”返回指定 JSON 字段与统一错误格式；仅下载成功返回二进制附件。错误优先级 SHALL 为 401 > 403 > 400/413/415 > 404 > 503；无法因依赖故障判定身份或对象时 MUST 直接返回 503，不伪造 401/404。当前非法文件类型仍为 400，415 为保留类别。

#### Scenario: Unified routes through Nginx
- **WHEN** 客户端通过 Nginx 请求上述路径
- **THEN** API 路径原样转发到 Go，成功读取/登录/登出返回 200，上传成功返回 201；API 错误保留约定状态码，不回退为 React HTML 页面

#### Scenario: Stable response fields
- **WHEN** 客户端登录、查询身份、上传并读取材料列表或详情
- **THEN** 登录响应为 user 对象，身份对象使用 id 而非 user_id；上传返回 material_id；列表为 items 数组，元素含 id/title/class_id/created_at；详情另含 text，空列表为 items:[]，均不泄露内部存储路径

#### Scenario: Safe error responses
- **WHEN** 任一接口产生错误，包括 Nginx 在 Go 不可达时响应 /health
- **THEN** 返回对应 HTTP 状态及仅含 error/message 的统一安全 JSON；跨班与不存在材料使用相同 not_found 消息，无材料数据、路径、凭据或字段校验细节

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
- **THEN** 超限返回 413，其余情况均返回 400，不创建材料或知识库记录，不保留最终文件

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

### Requirement: Credential and content safety
系统 MUST 仅持久化带随机盐的密码哈希，不将明文密码写入数据库、文件或日志；服务端会话索引摘要密钥 SESSION_SECRET MUST 从环境提供，缺失或空值时启动失败，不得存在默认密钥。会话表 MUST 仅保存会话 ID 的密钥摘要，Cookie 中的原始 ID 不得写入日志或 localStorage。材料展示 SHALL 转义文本，文件 SHALL 以附件下载，文件名不能控制存储路径；Cookie 写操作 MUST 受到 CSRF 保护。

#### Scenario: Inspect stored credentials
- **WHEN** 初始化账号并完成登录后检查数据库及应用日志
- **THEN** 密码字段仅为带盐哈希，日志没有密码、完整会话凭据或密钥

#### Scenario: Missing secret
- **WHEN** 环境未提供有效的非空 SESSION_SECRET 会话索引摘要密钥
- **THEN** 应用拒绝启动并提示缺失配置，不打印密钥内容

#### Scenario: Unsafe content and filename
- **WHEN** 教师上传含 HTML 脚本样式文本或路径片段文件名的合法 txt/md
- **THEN** 文件不能写到指定存储目录之外，页面显示文本而不执行脚本，下载以附件返回

#### Scenario: Forged write request
- **WHEN** 已登录会话向上传入口发送缺失或错误 CSRF 令牌的请求
- **THEN** 请求被拒绝为 403，不能写入文件或记录

#### Scenario: Anonymous CSRF bootstrap
- **WHEN** 无登录身份的客户端同源请求 GET /api/csrf
- **THEN** 返回 200 与 csrf_token 并设置绑定匿名会话的不透明 Cookie，匿名会话不能读取材料；客户端携带 Cookie 与 X-CSRF-Token 才能通过登录 CSRF 检查

#### Scenario: CSRF expiry and session replacement
- **WHEN** login/logout/upload 使用缺失、错误、超过 30 分钟或超过会话有效期的令牌，或使用会话替换前的旧令牌；受保护接口已通过身份检查
- **THEN** 返回 403 csrf_invalid，无业务副作用；登录成功或再次获取令牌后旧令牌失效，登出删除会话后旧令牌失效；前端必须重新获取绑定新会话的令牌

### Requirement: Local deployment health and persistence
系统 SHALL 采用 Go 后端、MySQL 8.0、React 18 TypeScript Vite 前端及 Nginx 入口，提供 Docker Compose 本地部署方案；数据库（含服务端会话表）与上传原文件 MUST 持久化在容器外的命名卷中。匿名 `GET /health` SHALL 只表示进程存活，Go 可响应时返回 200 与 {"status":"ok"}，MUST NOT 查询数据库、认证会话或检查存储。依赖数据库的业务接口在数据库不可用时 MUST 返回 503，不泄露业务数据或配置。

#### Scenario: Anonymous health check
- **WHEN** 无会话请求健康入口，分别模拟数据库/存储可用和不可用，Go 进程仍可响应
- **THEN** 均返回 200 与 {"status":"ok"}，不访问数据库或存储，响应没有材料、账号或密钥

#### Scenario: Business dependency outage and recovery
- **WHEN** 已登录后数据库停止，再请求 /health 与材料接口，随后恢复数据库
- **THEN** 故障期间 /health 为 200、材料接口为 503；恢复后有效会话可再次读取本班材料，业务恢复不依赖重建会话

#### Scenario: API process unavailable
- **WHEN** Go 进程停止，匿名客户端经 Nginx 请求 /health
- **THEN** 入口返回 503 与 {"error":"service_unavailable","message":"服务暂不可用"}，不得返回静态成功响应

#### Scenario: Recreate containers while preserving volumes
- **WHEN** 上传成功后停止并重建 Compose 容器，保留命名卷，再重新登录
- **THEN** 账号仍可登录、材料记录及关联文本仍在、原文件内容一致，班级隔离仍生效

#### Scenario: Session survives container recreation
- **WHEN** 未过期登录会话存在时重建容器，保留两个数据卷及相同 SESSION_SECRET，重建完成时会话仍未到期
- **THEN** 原 Cookie 仍可用于访问本班材料，无需重新登录；过期会话不会因重建而延长有效期

### Requirement: Reproducible class fixtures
系统 SHALL 提供 A/B 两班及教师 A（teacher_a）、学生 A1（student_a1）、学生 B1（student_b1）的开发验收初始化方式；密码从环境输入。样本 SHALL 包含 A 班待上传 txt/md 和 B 班预置材料，采用 design.md 所列不同标记，B 班材料必须具有原文件和知识库记录。重复初始化 MUST 不重复创建数据或重置现有密码。

材料 SHALL 使用 source=seed/upload 标记来源，B 班种子为 seed，教师上传为 upload。此处重复初始化指正常串行执行；并发初始化与种子执行中途失败补偿不在本迭代范围。

#### Scenario: Fixture isolation matrix
- **WHEN** 初始化完成，教师 A 上传两个 A 班样本，三个账号分别刷新列表并读取可见材料
- **THEN** 教师 A 与学生 A1 可见两个 A 班标记但不可见 B 班标记；学生 B1 仅可见 B 班标记且不能上传

#### Scenario: Initialize again
- **WHEN** 在相同数据卷上重复运行开发初始化
- **THEN** 班级、账号、B 班种子及其知识库记录不重复，已有密码哈希和上传材料不被覆盖
