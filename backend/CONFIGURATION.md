# 配置与忽略规则（T1.3）

根目录 `.env.example` 是配置清单，所有赋值均为空，没有真实或默认密码。它不是可直接启动的配置。当前 Go 不自动读取 `.env`，需由调用方提供进程环境变量；复制为本地 `.env` 后也不会自动生效。不要将密码粘贴到命令参数、终端历史或提交文件中。

## 当前读取项

| 变量 | 使用者 | 约束 |
| --- | --- | --- |
| SESSION_SECRET | server | 必需且非空；后续会话 HMAC 密钥，当前仅校验配置 |
| DB_HOST | server、migrate | 必需且非空 |
| DB_PORT | server、migrate | 必需，1–65535；MySQL 通常为 3306 |
| DB_NAME | server、migrate | 必需且非空 |
| DB_USER | server、migrate | 必需且非空，独立应用账号 |
| DB_PASSWORD | server、migrate | 必需且非空，原值不裁剪 |
| HTTP_ADDR | server | 可选，省略或空值默认 127.0.0.1:8080；主机加有效端口 |

必需项纯空白也视为缺失。SESSION_SECRET 原值同样不裁剪，由环境提供随机密钥，不设内置值。配置错误只报告变量名，不打印值、连接串或整个配置对象。当前启动与迁移使用安全错误文本；后续请求日志也不得记录密码、Cookie、会话 ID、CSRF token 或其摘要。不要将输出完整环境、shell 跟踪或 `docker compose config` 的日志作为验收附件。

## 后续任务的固定约定

三个独立种子密码变量为 `SEED_TEACHER_A_PASSWORD`、`SEED_STUDENT_A1_PASSWORD`、`SEED_STUDENT_B1_PASSWORD`，分别对应 teacher_a、student_a1、student_b1。它们预留给第 3 组显式初始化任务；当前 server/migrate 不读取、不校验，也不创建账号。不要将种子密码或服务端凭据放入 `VITE_*`、前端源码或构建参数。

会话固定 8 小时（28800 秒），不滑动续期；匿名会话 30 分钟（1800 秒）；CSRF 取 30 分钟与会话剩余时间的较小值，重新获取或替换/删除会话后旧 token 失效。这些是已评审的固定策略，本轮不新增 TTL 环境开关，也不实现会话逻辑。正常重建保留数据库、原密钥与到期时间后，未过期会话应继续有效；换密钥需重新登录。

| 计划命名卷 | 服务及挂载点 | 内容 |
| --- | --- | --- |
| mysql_data | MySQL `/var/lib/mysql` | 表、账号哈希、会话、材料和知识库记录 |
| material_data | Go `/data` | `/data/uploads` 原文件与 `/data/staging` 临时文件 |

Nginx 不挂载材料卷。正常停止/重建必须保留两个卷，不使用删除卷选项。Compose 和镜像在第 7 组实现；本轮没有创建卷或启动容器，也没有新增路径环境变量。`/health` 仅报告进程存活，不能据此认定数据库或上传存储就绪。

## Git 与构建上下文

根目录 `.gitignore` 排除各层级真实 `.env` / `.env.*`、私钥文件、secrets 目录、运行数据目录、日志和构建输出，允许 `.env.example`。规则不会移除已跟踪文件；提交前仍需检查暂存差异。不得强制添加被忽略的凭据。

根目录 `.dockerignore` 用于后续以仓库根目录为上下文的构建；连 `.env.example` 也不发送。排除凭据、运行数据、日志、Git 元数据和本地构建输出，保留 backend 源码、go.mod/go.sum 及 migrations。将来若使用其他上下文或 Dockerfile 专用 ignore 文件，必须重新验证其有效规则；当前不存在镜像构建结果。忽略规则不能发现藏在任意普通源码文件中的密钥。

前端应在将来的构建阶段生成产物，不依赖本机 dist/node_modules。真实值只在运行时注入，不使用 Docker ARG/COPY 搬运凭据。本轮验证配置清单、忽略规则及现有安全错误测试，记录见 [VERIFY-T1.3.md](VERIFY-T1.3.md)。
