# 配置与忽略规则（T1.3）

根目录 `.env.example` 是配置清单，所有赋值均为空，没有真实或默认密码。它不是可直接启动的配置。当前 Go 不自动读取 `.env`，需由调用方提供进程环境变量；复制为本地 `.env` 后也不会自动生效。不要将密码粘贴到命令参数、终端历史或提交文件中。

## 当前读取项

| 变量 | 使用者 | 约束 |
| --- | --- | --- |
| TOKEN_SECRET | server | 必需且非空，JWT 的 HS256 签名密钥；更换后全部未过期 token 立即失效 |
| DB_HOST | server、migrate | 必需且非空 |
| DB_PORT | server、migrate | 必需，1–65535；MySQL 通常为 3306 |
| DB_NAME | server、migrate | 必需且非空 |
| DB_USER | server、migrate | 必需且非空，独立应用账号 |
| DB_PASSWORD | server、migrate | 必需且非空，原值不裁剪 |
| HTTP_ADDR | server | 可选，省略或空值默认 127.0.0.1:8080；主机加有效端口 |
| UPLOAD_DIR | server、seed-materials | 必需且使用同一绝对目录；Compose 规划 /data/uploads；配置及 health 不访问磁盘 |
| MYSQL_ROOT_PASSWORD | Compose 的 mysql 服务 | 首次初始化必需；不传给 API 或前端 |
| CAMPUSCLAW_PORT | Compose 的 nginx 服务 | 可选，默认 8080；仅绑定 127.0.0.1 |

两个初始化命令也读取全部 DB_*；seed-materials 不需种子密码或 TOKEN_SECRET，先执行迁移和账号初始化。UPLOAD_DIR 是受信任的运维配置，不是请求参数。UPLOAD_DIR 及其同级 staging 必须专用于本应用；Go 启动会清理临时文件和无引用原文件，不可配置为共享目录，不同时运行初始化或其他 Go 实例。恢复失败时业务 503，修复依赖后人工重启，详见 UPLOAD.md。此表中的材料目录配置自 T3.2 起生效，T1.3 验证记录保留当时的 10 项配置快照。

必需项纯空白也视为缺失。TOKEN_SECRET 原值同样不裁剪，由环境提供随机密钥，不设内置值。配置错误只报告变量名，不打印值、连接串或整个配置对象。当前启动与迁移使用安全错误文本；后续请求日志也不得记录密码、token 原文或其签名。不要将输出完整环境、shell 跟踪或 `docker compose config` 的日志作为验收附件。

## 初始化与固定约定

数据库连接时区固定为 UTC，与 Go 的 DATETIME 解析一致，created_at 返回 UTC RFC3339；页面按浏览器时区显示。T6 修正仅影响此后数据库连接，不推测或批量改写历史记录时区。

三个独立种子密码变量为 SEED_TEACHER_A_PASSWORD、SEED_STUDENT_A1_PASSWORD、SEED_STUDENT_B1_PASSWORD，分别对应 teacher_a、student_a1、student_b1。cmd/seed 读取它们，另需上表全部 DB_*，不需 TOKEN_SECRET。密码必须为有效 UTF-8、1–128 码点且不能纯空白；其余原值保留，在数据库访问前全部校验。重复执行也必须提供密码，但不重置旧密码。server/migrate 不读取种子密码；禁止将服务端凭据放入 VITE_* 或前端产物。

token 有效期固定 8 小时，不滑动续期，无 TTL 环境开关；不再有匿名会话与 CSRF 令牌期限。正常重建保留数据库与原 TOKEN_SECRET 后，未过期 token 继续有效且无需重新登录；更换密钥则全部 token 立即失效。

| Compose 命名卷 | 服务及挂载点 | 内容 |
| --- | --- | --- |
| mysql_data | MySQL `/var/lib/mysql` | 表、账号哈希、材料和知识库记录（迁移 000002 后不再有会话表） |
| material_data | Go `/data` | `/data/uploads` 原文件与 `/data/staging` 临时文件 |

Nginx 不挂载材料卷。正常停止/重建必须保留两个卷，不使用删除卷选项。Compose、镜像构建与两个命名卷已在隔离 Docker 项目验证，停机重建和备份恢复通过，测试结束后停止容器并保留测试卷。本地材料初始化使用 T3.2 新增的 UPLOAD_DIR。`/health` 仅报告进程存活，不能据此认定数据库或上传存储就绪。部署及恢复见 [部署说明](../deploy/README.md)。

## Git 与构建上下文

根目录 `.gitignore` 排除各层级真实 `.env` / `.env.*`、私钥文件、secrets 目录、运行数据目录、日志和构建输出，允许 `.env.example`。规则不会移除已跟踪文件；提交前仍需检查暂存差异。不得强制添加被忽略的凭据。

根目录 `.dockerignore` 用于以仓库根目录为上下文的构建；连 `.env.example` 也不发送。排除凭据、运行数据、日志、Git 元数据和本地构建输出，保留 backend 源码、go.mod/go.sum 及 migrations。将来若使用其他上下文或 Dockerfile 专用 ignore 文件，必须重新验证其有效规则；第 7 组已完成实际镜像构建。忽略规则不能发现藏在任意普通源码文件中的密钥。

前端在容器构建阶段生成产物，不依赖本机 dist/node_modules。真实值只在运行时注入，不使用 Docker ARG/COPY 搬运凭据。本轮验证配置清单、忽略规则及现有安全错误测试，记录见 [VERIFY-T1.3.md](VERIFY-T1.3.md)。
