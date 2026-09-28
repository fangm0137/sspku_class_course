# Design

## Context

动机见 proposal.md。已检查 README、AGENTS.md、OpenSpec 配置与目录：当前没有业务源码、测试、部署文件或主规约；README 已指向本变更目录。本轮仅规划，以下均为后续实现决策，不代表已实现或已验证。

## Goals / Non-Goals

**Goals:** 在单机课堂演示中形成登录、可信授权、上传入库、同班读取的完整闭环，并能通过 API 直接验证，权限不依赖页面按钮。

**Non-Goals:** 沿用 proposal 的排除项：问答、聊天、作业流程、注册找回、SSO、生产高可用。无需向量数据库、嵌入模型、异步任务集群或跨班级管理功能。

另外不做并发初始化、种子中途失败补偿、登录限流、分页、总存储配额、文件清理持续失败的自动重试策略，以及上传去重/幂等键。非目标不免除正常串行种子幂等和上传事务回滚要求。

## Decisions

### 1. 教程技术栈与本地持久化

依据 `docs/第三课讲义.md` 第 1.3 节与第 12 节，采用 Go 后端、MySQL 8.0、React 18 + TypeScript + Vite 前端和 Nginx 入口。Go 使用 `net/http` 提供 API，通过 `database/sql` 与 MySQL 驱动访问数据库；Vite 构建的 React 静态产物由 Nginx 托管。选择该组合是为了与后续课程实现保持一致。

Compose 规划三个服务：Nginx、Go API、MySQL。浏览器通过 Nginx 同源访问 React 页面、`/api/` 和 `/health`；后两者保留路径转发到 Go，API 错误不能回退成 React 页面。仅 Nginx 向 localhost 暴露端口，Go 与 MySQL 使用内部网络。Nginx 不挂载上传文件卷，不提供材料静态下载；所有材料下载必须经过 Go 授权。代理请求体上限需容纳 5 MiB 文件及 multipart 开销，文件大小和业务错误由 Go 按规约校验。

MySQL 使用 InnoDB，数据目录 `/var/lib/mysql` 挂载独立命名卷 `mysql_data`；Go 的 `/data` 挂载 `material_data`，保存 `/data/uploads` 原文件与 `/data/staging` 临时文件。容器镜像和临时可写层不保存业务数据。数据库连接配置及密码从环境注入，不写入前端构建产物；Go 使用独立应用数据库账号，不使用 root 账号。

T1.1 配置落点：backend 为 Go module，入口为 cmd/server。必需环境变量为 SESSION_SECRET、DB_HOST、DB_PORT、DB_NAME、DB_USER、DB_PASSWORD，缺失、空值或纯空白时启动失败，只报告变量名；DB_PORT 必须为 1–65535。HTTP_ADDR 可选，默认 127.0.0.1:8080，可显式配置内部容器监听地址；端口同样必须有效。保留密码/密钥原值，不自动裁剪。使用 go-sql-driver/mysql 构造延迟连接池，配置加载和 /health 均不执行数据库 ping；后续任务负责数据库结构与业务就绪检查。本轮不创建数据库表、认证或材料业务。

T1.3 配置清单放在根目录 .env.example，全部赋值留空；当前 Go 不自动加载 .env。除上述 7 个读取项外，预留 SEED_TEACHER_A_PASSWORD、SEED_STUDENT_A1_PASSWORD、SEED_STUDENT_B1_PASSWORD 给第 3 组初始化，当前命令不读取。会话 8 小时、匿名会话/CSRF 30 分钟及卷内路径沿用固定策略，不新增环境开关。根目录 .gitignore 排除各层级本地凭据、数据和构建产物（允许 .env.example）；.dockerignore 用于仓库根构建上下文，连示例也排除，但保留 Go 源码与迁移 SQL。Dockerfile 不通过 ARG/COPY 注入凭据、不依赖本地前端产物；第 7 组已实现并验证镜像构建。详细配置与验证边界见 backend/CONFIGURATION.md。

### 2. 身份、会话与密码

预置账号包含唯一用户名、带随机盐的 scrypt 密码哈希、角色（teacher/student）、单一班级外键。单账号单班级是本迭代的数据模型约定。密码通过后续初始化时的环境变量提供，禁止源码默认密码及明文落库，日志不得记录密码或会话凭据。

使用 MySQL 服务端会话表 `sessions`，Cookie 仅携带密码学安全随机源生成的 32 字节不透明会话 ID，不携带用户标识、角色、班级或可自验证的权限载荷。会话表保存 ID 的 HMAC-SHA256 摘要、user_id 外键、created_at、expires_at；HMAC 密钥 `SESSION_SECRET` 从环境注入，缺失或空值则启动失败，禁止默认值。它用于服务端会话索引摘要，不用于在 Cookie 中签发身份数据。每次请求先查有效会话，再从账号表取得最新角色与班级；有效期固定为登录后 8 小时，不自动续期。不存在、被删除或过期的会话返回 401；查库失败返回 503，不能降级放行或误判为 401。

登录成功时新建会话并删除本次请求携带的旧会话，事务提交后才设置 Cookie，防止会话固定。`POST /api/logout` 删除当前服务端会话并清 Cookie；删除失败返回 503，不能宣称退出成功。`GET /api/me` 返回 id、username、role、class_id，供前端刷新时恢复身份；其中 id 是用户 ID，不返回会话摘要或密码字段。Cookie 名称为 `campusclaw_session`，Path=/，设置 HttpOnly、SameSite=Lax；课堂仅在 localhost HTTP 演示，HTTPS 环境启用 Secure。

CSRF 合同：前端先同源请求匿名可访问的 `GET /api/csrf`。没有有效 Cookie 时创建 user_id=NULL、有效期 30 分钟的匿名 sessions 行，并设置上述不透明 Cookie；匿名会话不具备材料读取权限。有效登录会话则使用原 sessions 行。接口生成随机 32 字节 token，在 JSON 的 csrf_token 字段返回 Base64URL 字符串；仅保存 SHA-256 摘要 csrf_token_hash 和 csrf_expires_at。令牌有效期为 30 分钟与当前会话剩余时间的较小值，响应设置 Cache-Control: no-store，不允许跨源读取。再次获取令牌会替换旧令牌。

login/logout/upload 必须携带 `X-CSRF-Token` 请求头并通过同源校验；不支持表单字段替代。服务端核对 Cookie 对应行的 token 摘要和到期时间，失败返回 403 csrf_invalid。登录成功替换匿名或旧登录会话后，旧 token 随旧行失效；前端重新调用 /api/csrf 获取新 token 后才能上传或登出。登出删除会话后旧 token 同样失效。缺登录身份的受保护接口优先返回 401；匿名登录的 CSRF 校验不要求已有登录身份。查会话或写 token 时数据库不可用返回 503。令牌及会话 ID 均不得写入日志。

T2.1 同源细化：写请求必须携带单一合法 Origin，按协议、主机、有效端口与当前请求匹配（默认端口归一化），缺失、null、多值或不匹配均为 403 csrf_invalid；不以 Referer 或表单 token 替代。GET /api/csrf 允许无 Origin 的同源浏览器/命令行访问；若带 Origin 则必须匹配。Sec-Fetch-Site 如存在，只接受 same-origin 或 none；其余拒绝 403。上述拒绝不访问数据库，受保护写请求仍先验证身份。协议由直接连接 TLS 决定，不信任 Forwarded/X-Forwarded-*；第 7 组 localhost HTTP Nginx 必须保留外部 Host，未来 TLS 终止代理需另行约定可信来源。令牌接口成功和失败都 no-store，不设置跨源许可头。会话 ID 和 token 均使用无填充 Base64URL 编码的随机 32 字节，摘要分别基于解码字节计算 HMAC-SHA256/SHA-256。到期时刻等于当前时间即失效。令牌轮换在事务中锁定会话行，提交后才返回 token 或新 Cookie；重复获取不会延长原会话期限。

登录字段边界：请求必须为包含 username/password 字符串的 JSON 对象；username 长度为 1–64 个 Unicode 码点且不能全为空白，password 为 1–128 个 Unicode 码点。缺失、null、空串、超长、类型错误或非法 JSON 统一为 400 invalid_request；不回显字段名、长度或具体校验细节，不自动裁剪密码。格式合法后才做密码校验，错误凭据为 401 invalid_credentials。

T2.2 登录解析细化：Content-Type 必须为 application/json（可附 charset）；请求体最多 16 KiB，超限仍为通用 400 invalid_request。必须只有一个 JSON 对象，拒绝重复字段名、尾随第二个值和非法 UTF-8；仅使用精确小写 username/password，额外字段被忽略且不能赋予角色或班级。字符串不裁剪、不做 Unicode 归一化。密码哈希规范为 $scrypt$32768$8$1$<16字节盐的无填充Base64>$<32字节派生值的无填充Base64>，参数固定、比较使用恒定时间；不接受任意数据库参数驱动的大内存计算。不存在账号或损坏/不支持的哈希均执行同成本占位计算后返回相同 401，不设默认密码。此前迁移测试中的 ln=14 格式仅为独立结构 fixture，不是产品账号。登录事务锁定并再次检查旧会话及 token，只有仍有效才替换，防止并发复用旧会话重复登录成功；失败回滚，不发新 Cookie。登录成功的新会话不复制旧 CSRF token，必须重新获取。

选择服务端会话而非客户端自包含会话，是为了支持即时吊销及登出后拒绝旧 Cookie 重放；代价是每次认证依赖 MySQL。账号不存在和密码错误均返回相同 401 信息，不泄露账号存在性。会话 ID 不进入 localStorage 或日志。

### 3. 接口与授权顺序

| 入口 | 行为 |
| --- | --- |
| `GET /api/csrf` | 匿名可访问，200 下发绑定当前匿名/登录会话的令牌；跨源 403，数据库不可用 503 |
| `POST /api/login` | 校验用户名/密码，成功 200 并建立会话；失败 401 |
| `POST /api/logout` | 有效会话及 CSRF 保护；删除会话并清 Cookie，成功 200；无效会话 401 |
| `GET /api/me` | 有效会话返回 200 及当前身份、角色、班级；无效会话 401 |
| `GET /api/materials` | 登录后返回本班列表，空列表为 200；无跨班级计数 |
| `POST /api/materials` | multipart 单文件上传，先认证再检查 teacher，成功 201 返回材料 ID |
| `GET /api/materials/{id}` | 返回本班材料元数据及关联提取文本 |
| `GET /api/materials/{id}/file` | 受保护下载原文件，以附件返回 |
| `GET /health` | 匿名进程存活检查；Go 能响应即 200，不查询数据库或文件存储 |

保留讲义第 12 节的八个路径，另增加本次明确批准的 /api/csrf，不增加路径别名。登录请求为 JSON username/password；上传使用 multipart 字段 file。成功与失败结构见“接口响应约定”。依赖数据库的业务接口在查询/写入不可用时返回 503；前端显示服务暂不可用，不将 503 当作会话失效，也不清除当前身份。

所有材料入口先检查身份，失败返回 401；上传再检查角色，学生返回 403，之后才处理文件。客户端提供的 `class_id`（查询串、表单或 JSON）以及角色参数一律不作为授权依据；忽略这些参数，以账号班级执行操作。教师 A 携带 B 班参数仍只能上传到 A 班。详情与下载使用 `id + 当前班级` 联合过滤，不存在或跨班均为同样的 404；不通过先查全局记录后返回差异错误来暴露存在性。

知识库不提供独立公开 API，只通过已授权的材料详情读取提取文本。文件不放在静态目录，不开放存储路径或直接下载 URL。React 最小页面提供登录、登出、材料列表、教师上传和详情/下载；刷新时调用 /api/me 恢复身份，通过 Nginx 同源调用 Go API 并携带不透明会话 Cookie 与请求保护令牌。隐藏学生上传入口只是交互提示，后端检查才是安全边界。

T4.1–T4.3 读取细化：本轮经用户授权合并验收三个关联任务。材料路由先通过会话关联 users 读取最新身份并写入服务器请求上下文；教师门禁在此之后判断 role，不读取请求正文或执行文件写入。材料 ID 必须为无前导零的正十进制 uint64，否则在认证后返回同形 404；不存在/跨班一致。列表固定按 id 升序返回全部本班材料，无分页。server 现在也要求绝对 UPLOAD_DIR，与 seed-materials 使用同一目录；配置只检查字符串，不检查磁盘，因此 health 不依赖目录可用性。下载先按 id/class_id 查元数据，再以受限目录打开内部单层存储键；拒绝路径片段、符号链接和非普通文件，缺失/不可读/内容长度异常返回安全 500，成功发送前先完整读取至多 5 MiB。title/附件名均去掉路径片段与控制字符；响应 no-store，附件为 application/octet-stream。详情已授权材料缺少关联知识库行为 500，数据库查询错误为 503。不注册静态目录或独立知识库路由，未注册路径返回安全 JSON 404。T4.1 教师门禁仅用测试 handler 验收，POST /api/materials 仍留待上传任务。

T6.1–T6.2 前端实现细化：本轮合并验收前端组。首次加载以 /api/me 恢复身份，401 显示登录，503/网络错误保持未知状态并提供重试，不能误判退出；已登录遇到 503 保留内存身份，只有明确的受保护请求 401 清除身份与材料。Cookie 由浏览器管理，不读取或写入 Web Storage；token 仅保存在内存。每次用户发起写操作先获取新 token，登录成功后再次获取绑定新会话的 token；403 提示用户重新操作，不自动重放上传。上传请求结果未知时提示先刷新确认，避免无意重复上传。详情仅用 React 文本节点/pre 展示源码，附件通过受保护 API 获取 Blob 后下载，不渲染 HTML。操作期间禁止重复按钮提交，上传成功刷新 items 并展示 material_id 对应详情。

开发与本轮浏览器验收使用 localhost Vite 同源代理 /api 与 /health 到 Go，保留 Host，不更改后端同源校验；仅开发进程读取非凭据 CAMPUSCLAW_API_TARGET，默认 http://127.0.0.1:8080，客户端全部使用相对地址。生产 Nginx/Compose 仍属第 7 组，不在本轮实现。构建不加载环境文件，不定义 VITE_* 服务端凭据变量，依赖锁文件纳入版本管理。浏览器测试使用独立 MySQL、随机一次性凭据及浏览器上下文，不保存 trace、会话快照或登录过程录像。

T6 页面验收发现 MySQL 默认本地时区与 Go 按 UTC 解析 DATETIME 不一致；数据库连接统一设置 session time_zone='+00:00'，保证新材料 created_at 的 UTC RFC3339 值对应真实上传时刻。前端按浏览器本地时区显示，不改变历史数据库行；本轮不做无法判定原始时区的历史时间迁移。

### 4. 上传合同与一致性

接受扩展名大小写不敏感的 `.txt`、`.md`，严格按 UTF-8（允许 BOM）解码。Markdown 提取文本定义为解码后的源码文本，不渲染为 HTML，不调用模型。单文件最大 5 MiB（5 × 1024 × 1024 字节）；按速查表统一非法类型、缺文件/多文件/空白文本/非法编码为 400，超限为 413。认证、授权优先于这些业务校验。客户端文件名仅保留安全展示名，存储名由服务端随机生成，不能包含用户指定路径。

数据表规划：classes（id、name）；users（id、username 唯一、class_id、role、password_hash）；sessions（session_id_hash 唯一、user_id 可空外键、created_at、expires_at、csrf_token_hash、csrf_expires_at）；materials（id、class_id 非空并建索引、uploader_id、source、original_name、storage_key、size、created_at）；knowledge_entries（id、material_id 唯一外键、class_id 非空并建索引、extracted_text）。sessions 保存会话 ID 的 HMAC 摘要，不保存原始 ID。source 枚举为 seed/upload：B 班预置材料为 seed，允许 uploader_id 为空；教师上传为 upload，uploader_id 必须关联教师。按 T1.2 新要求，知识库显式保存 class_id，使用 (material_id, class_id) 到 materials(id, class_id) 的复合外键防止两份班级归属漂移，材料仍为权限来源。API 的 title 由安全展示名 original_name 派生，不另建标题输入流程。

T1.2 使用 golang-migrate 的版本化显式 SQL（up/down）及 schema_migrations 元数据表，无 ORM 自动建表；连接参数来自 DB_* 环境变量，验证插入通过占位参数传值，不拼接外部输入到 SQL。独立迁移命令不需要 SESSION_SECRET，不由 health 或 HTTP 启动自动触发。MySQL 8.0.16+ 强制执行 CHECK；应用验收使用 MySQL 8.0。迁移首次 up 建表，重复 up 为无变更；DDL 失败保留 dirty 状态，禁止自动 force 或宣称 DDL 可整体回滚。down 仅供显式销毁已备份/可丢弃数据的数据库，正常运行不调用。

密码仅设 password_hash 列，采用 $scrypt$ 编码格式；数据库检查格式前缀/长度，不宣称能从 SQL 证明输入经过真实密码哈希。实际哈希计算属于后续账号初始化/认证任务，T1.2 使用随机测试输入计算 scrypt 后验证存储。当前 change 与主规约中均无讲义、作业、助手、技能占位表定义，因此“仍保留的占位结构”集合为空，不新增这些业务表。本轮只建五张业务表和迁移元数据，不实现种子（当前编号已调整为 3.x）或业务接口。

执行流程：授权 → 校验并写临时文件 → 解码提取 → 数据库事务内创建材料与知识库记录 → 将文件移动到持久目录 → 提交事务 → 返回 201。同步完成后列表即可读，无等待后台索引。确定失败且未提交时两种记录均不提交，不产生可见半成品；存储/提取内部异常返回通用 500，数据库不可用返回 503。一次补偿删除失败时保留待清理文件，启动时清理 staging 与无数据库引用的文件，业务就绪前完成清理；health 不等待数据库或清理。持续清理失败时保持业务未就绪（业务接口 503），记录不含凭据的错误并等待人工处理，不实现自动重试策略。事务已提交但响应中断、客户端未收到 201 时，原文件与两种记录仍有效且可查询；客户端重试视为独立上传，本迭代不提供去重或幂等键。

选择数据库事务加文件补偿，而非宣称文件系统与数据库原子提交；Go 在同一个 MySQL InnoDB 事务中写入两类记录，启动清理解决提交前崩溃产生的孤儿文件。MySQL 提交时若连接中断导致结果不确定，必须按本次服务端生成的材料 ID 重新核实提交结果；无法核实时保留文件并返回 503，待数据库恢复后核实再清理，不能误删已提交记录引用的文件。

T5.1–T5.4 实现细化：本轮合并完成上传组并分别验收。multipart/form-data 只接受一个名为 file 的文件；其他普通字段（含 class_id/role）忽略，错误字段名的文件、多文件、损坏的 multipart 返回 400。请求总大小最多 5 MiB + 64 KiB，文件单独仍限 5 MiB，超过返回 413。安全展示名最多 255 个 Unicode 码点，超出为 400；BOM 保留在原文件中，提取文本去掉首个 UTF-8 BOM 后验证非空白。

临时目录使用 UPLOAD_DIR 同级 staging；两个目录专用于本应用，不与运行中的种子初始化或另一个 Go 实例同时清理。文件随机命名并独占创建；最终上传键包含材料 ID 和随机部分供恢复核对。先完成临时写入、Sync 与提取，再执行两表事务、移动文件与提交。提交错误保守视为未知：保留文件，返回 503 并关闭业务就绪门禁；人工恢复依赖后重启 Go，执行一次核实/清理，不自动重试。恢复对最终文件按存储键加锁查询（FOR UPDATE 等待未决事务结束），核对键中的材料 ID；有引用保留，无引用才删除。无法核实或清理失败保持业务 503；确定失败执行一次补偿，补偿失败同样关闭门禁。启动清理在独立 goroutine 中执行一次，完成前所有业务入口 503，health 独立。普通数据库故障不关闭已就绪门禁，恢复后请求可重试；文件一致性待恢复时才要求人工重启。

### 5. 健康检查、配置与样本

T7 部署细化：Compose 仅定义 mysql、api、nginx 三服务，只有 Nginx 绑定 127.0.0.1 的入口端口。mysql_data 保存 /var/lib/mysql，material_data 保存 API 的 /data（含 uploads/staging）；Nginx 不挂载材料卷。MYSQL_ROOT_PASSWORD 是仅 MySQL 初始化使用的新必需环境项，不传给 API；API 使用独立 DB_USER/DB_PASSWORD。Compose 文件读取环境，但镜像构建不接收凭据。镜像多阶段编译 Go 与 Vite，API 使用非 root 用户。

初始化流程显式串行：构建 → 仅启动 MySQL 并等待独立 SELECT 1 就绪检查 → 一次性 API 容器执行 migrate、seed、seed-materials → 启动 API/Nginx。不得在运行的 API 旁执行种子清理；正常重建不自动重置密码或执行种子。API 存活检查仅请求 /health；Nginx 保留外部 Host，并使用 Docker DNS 动态解析 API，容器重建换 IP 后仍可转发。九个路由经 /api/ 与精确 /health 转发，不改写为页面；上游无法连接/超时映射安全 JSON 503。Nginx 关闭请求体预缓冲及自身大小拒绝，由 Go 在认证/授权后实施 5 MiB + 64 KiB 总请求上限，避免代理提前 413 打乱身份优先级；合法 5 MiB 上传不被代理拦截。不自动重试写请求。

备份要求在停写状态同时保存 MySQL 逻辑备份和 material_data，并单独安全保管原 SESSION_SECRET；恢复到全新隔离项目/卷后核对两类数据与原会话。正常 stop/down 不删除卷，禁止将 down -v 当作停止步骤。尚无 Docker Engine 时只能完成配置/原生测试，容器运行与重建验证必须显式标记未执行。

Go 的 `GET /health` 仅表示进程存活，固定返回 200 与 `{"status":"ok"}`，不执行数据库查询、会话认证或目录读写检查。数据库停止或存储异常时，只要 Go HTTP 服务仍可响应，该入口仍返回 200；业务接口按依赖故障返回 503（内部文件操作故障沿用 500），不能以 health 成功推断业务可用。Nginx 将匿名请求代理到 Go；Go 不可达时由 Nginx 按统一错误格式返回 503 service_unavailable，不可用静态 200 掩盖上游退出。

Compose 对 MySQL 独立做就绪检查，Go 与 Nginx 使用存活检查；数据库就绪与业务初始化门禁不依赖 /health 判定。必需配置缺失仍停止启动；配置完整后，Go 可先响应 /health，待数据库结构初始化及文件恢复完成后再开放业务接口，初始化未完成时业务返回 503。运行中数据库中断不退出 Go 进程；恢复后业务重新可用。开发种子初始化另需三个账号密码。后续 `.env.example` 仅写变量名/占位说明；真实 `.env` 不提交。

验收数据由显式开发初始化步骤创建：A/B 两班；`teacher_a`（教师 A，A 班）、`student_a1`（学生 A1，A 班）、`student_b1`（学生 B1，B 班）。三者密码由环境分别注入，重复初始化不得重置账号密码或重复插入材料。B 班通过种子直接入库，不增加教师 B 或绕过学生上传限制的 API。

T3.1 使用独立命令 go run ./cmd/seed，仅创建账号与班级，不随 HTTP 启动执行，不自动迁移。三项 SEED_*_PASSWORD 在任何数据库访问前全部校验：必填、有效 UTF-8、1–128 码点，不允许纯空白，保留其余原值；错误仅报告变量名。该命令另需 DB_*，不需 SESSION_SECRET。班级以 name=A/B 查找，缺失时插入并使用数据库分配的 ID，不能假定 ID 为 1/2；同名多行或同名账号的角色/班级不符时报安全冲突错误，不覆盖已有数据。串行重复执行保留已有账号 ID、哈希和归属；即使提供不同的有效密码也不重置密码。新账号使用 T2.2 固定 scrypt 格式和各自随机盐。本轮以一次 MySQL 事务执行账号/班级初始化，不提供并发初始化或跨进程中断后的补偿/重试机制，也不创建材料、知识库或会话。

| 样本 | 用途 | 可辨识文本 |
| --- | --- | --- |
| `class-a-intro.txt` | 教师 A 实际上传 | `CLASS_A_TXT_ONLY` |
| `class-a-notes.md` | 教师 A 实际上传 | `# A notes` 与 `CLASS_A_MD_ONLY` |
| `class-b-seed.txt` | B 班预置材料、原文件及知识库记录 | `CLASS_B_SEED_ONLY` |

T3.2 样本位于 backend/fixtures，采用 UTF-8/LF，随 Go 二进制嵌入。两个 A 班文件仅为待上传源文件，不自动入库。账号初始化后另行运行 go run ./cmd/seed-materials，需 DB_* 与绝对目录 UPLOAD_DIR（Compose 时为 /data/uploads）；不需要账号密码或 SESSION_SECRET。B 班必须已唯一存在，只创建 source=seed、uploader_id=NULL 的 class-b-seed.txt，固定保留存储键 seed-class-b-v1.txt，用于串行幂等识别；教师上传仍使用后续随机键。原文件独占创建，两表在同一事务写入，提取文本等于样本 UTF-8 原文。重复执行核对班级、元数据、正文和原文件完全一致后返回 no change，不改 ID、时间或文件。已有记录不完整、文件缺失/不同或仅存在无记录文件时返回安全错误，既不覆盖也不自动修复；失败/中断后的遗留文件需人工核实处理，不实现非目标中的种子中断补偿或并发初始化。验收双向隔离仍由后续读取任务验证，不能用空 B 班列表代替。

## 接口响应约定

除原文件下载成功返回二进制附件外，所有 API 成功/失败响应均为 `application/json`。以下花括号中的带类型字段是结构定义，不是实际凭据或验收数据；未列出的内部字段不得输出。id、class_id、material_id 均为正整数，created_at 为 UTC RFC3339 字符串，role 为 teacher/student。

| 接口 | 成功状态与 JSON/内容 | 失败状态 |
| --- | --- | --- |
| GET /api/csrf | 200：`{"csrf_token":"<string>"}`，仅该安全入口可返回 CSRF token | 403（跨源）、503 |
| POST /api/login | 200：`{"user":{"id":<int>,"username":"<string>","role":"<role>","class_id":<int>}}`，同时设置新 Cookie | 400、401、403、503 |
| POST /api/logout | 200：`{"ok":true}`，同时清 Cookie | 401、403、503 |
| GET /api/me | 200：`{"id":<int>,"username":"<string>","role":"<role>","class_id":<int>}` | 401、503 |
| POST /api/materials | 201：`{"material_id":<int>}` | 400、401、403、413、500、503 |
| GET /api/materials | 200：`{"items":[{"id":<int>,"title":"<string>","class_id":<int>,"created_at":"<timestamp>"}]}`；空结果 `{"items":[]}`，无分页及 total 字段 | 401、503 |
| GET /api/materials/{id} | 200：`{"id":<int>,"title":"<string>","class_id":<int>,"created_at":"<timestamp>","text":"<string>"}`；text 为提取源码文本 | 401、404、503；关联正文意外缺失为 500 |
| GET /api/materials/{id}/file | 200：原始文件字节，Content-Type 为 application/octet-stream，Content-Disposition 为 attachment 并使用安全展示文件名 | 401、404、503；授权后原文件缺失/不可读且未发送响应时为 500 |
| GET /health | 200：`{"status":"ok"}` | Go 不可达时 Nginx 返回 503 |

所有错误响应统一为 `{"error":"<code>","message":"<safe text>"}`，包括 Nginx 的 /health 503；不含材料数据、内部路径、凭据或字段校验细节。固定代码及消息：400 invalid_request / 请求无效；401 invalid_credentials / 账号或密码错误（仅登录凭据失败），其余 401 unauthenticated / 请先登录；403 csrf_invalid / 请求校验失败，角色拒绝为 forbidden / 无权执行此操作；404 not_found / 资源不存在；413 payload_too_large / 文件超过大小限制；500 internal_error / 服务内部错误；503 service_unavailable / 服务暂不可用。跨班和不存在对象使用完全相同的 404 格式与消息。

错误优先级为 **401 > 403 > 400/413/415 > 404 > 503**，用于已经能够判定的业务错误。401 是受保护入口的身份检查，403 包括角色和 CSRF 检查；匿名登录不要求登录身份，先检查 CSRF，再检查输入格式，最后验证密码。若查会话或对象必须访问数据库而数据库故障，无法判定 401/404 时立即返回 503，不能为套用优先级伪造未登录或不存在。本轮非法文件类型仍为 400；415 仅是优先级中的保留类别，当前接口不新增 415 分支。500 用于未被上述业务错误覆盖的内部故障。

ID 非法格式、并发上传和代理超限等组合边界按本轮决定留到相关 task，确需改变外部行为时先补规约再实现；本轮不据此新增功能。

## Risks / Trade-offs

- 文件与数据库不能共同事务提交 → 返回成功前完成全部步骤；失败补偿与启动孤儿清理，并注入失败验证。
- MySQL 增加启动依赖及网络故障情形 → 独立数据库就绪检查、连接超时与事务结果核实；/health 仅报告进程存活，维持单个 Go 实例，生产高可用不在范围内。
- Nginx 代理可能改写 API 路径、错误码或绕过材料权限 → 保留 API 路径和错误响应，只托管 React 静态资源，端到端验收通过 Nginx 执行。
- Markdown 中可能包含脚本样式文本 → 只以转义文本显示，下载使用附件方式。
- 误删命名卷将丢失数据 → 文档区分正常停止/重建与显式删除卷；验收不使用删除卷选项。
- 会话索引摘要密钥更换使原会话 ID 无法匹配 → 重新登录并清理旧会话行；材料与账号哈希保持有效。正常容器重建保留会话表、密钥及过期时间，未过期会话仍可核验。

## Migration Plan

部署流程：构建 Go 可执行文件与 React 静态产物，配置 Nginx 路由，创建两个命名卷并等待 MySQL 就绪后初始化数据库结构，再从环境初始化验收账号和 B 班种子，由教师 A 经 Nginx 上传样本。验证停止并重建全部容器（保留两个命名卷）后账号、材料原文件、文本关联仍在。第 7 组已在隔离项目完成构建、初始化、5 MiB 上传下载、保留卷重建及备份恢复；第 8 组继续三账号完整验收。首次部署无需旧格式转换；后续升级前暂停写入，制作一致的 MySQL 备份与上传目录备份，失败时恢复匹配版本的镜像与数据备份，不删除卷。
