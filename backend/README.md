# Backend — T1.1–T5.4

三账号、样本、环境密码输入及初始化顺序见 [验收账号与材料样本](FIXTURES.md)。材料读取与班级隔离见 [MATERIALS.md](MATERIALS.md)，教师上传、事务补偿和恢复见 [UPLOAD.md](UPLOAD.md)，真实证据见 [VERIFY-T5.md](VERIFY-T5.md)。

当前已实现 Go 服务、配置、MySQL 迁移、显式账号及 B 班材料初始化、三个材料样本、health、login、me、logout，以及材料上传、列表、详情和附件下载。认证采用无状态 JWT：登录签发 8 小时 token，受保护接口只认 `Authorization: Bearer`，不再有会话 Cookie 与 CSRF。[React 页面](../frontend/README.md)与 Compose/Nginx 已接入；health 成功不代表业务就绪。启动先在后台执行一次文件恢复，完成前业务 503；恢复失败需人工处理后重启。认证使用见 [AUTH.md](AUTH.md)，token 结构与校验见 [TOKEN.md](TOKEN.md)。

模块当前最低要求 Go 1.26.0（新增依赖要求）；本轮使用 Go 1.27.1 验证，MySQL 驱动固定为 v1.10.1，golang-migrate 为 v4.20.1。在本目录执行：

```powershell
go mod download
go test ./...
go build ./...
```

必需环境变量：`TOKEN_SECRET`、`DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`、`UPLOAD_DIR`。缺失、空值或纯空白时进程非零退出，仅报告缺失变量名。`DB_PORT` 范围为 1–65535，`UPLOAD_DIR` 为与材料初始化相同的绝对目录。真实凭据由运行环境提供，不写入源码或提交到版本库；当前不会自动加载 `.env`。

可选 `HTTP_ADDR` 默认为 `127.0.0.1:8080`。配置好环境后，在本目录运行 `go run ./cmd/server`，在另一个终端请求：

```powershell
curl.exe -i http://127.0.0.1:8080/health
```

预期 HTTP 200、Content-Type 为 application/json、正文 `{"status":"ok"}`。该接口不访问数据库和文件，也不校验 token。按 Ctrl+C 停止前台服务。配置见 [根目录示例](../.env.example) 与 [CONFIGURATION.md](CONFIGURATION.md)，Compose 属于第 7 组任务。

本轮实际验证记录见 [VERIFY-T1.1.md](VERIFY-T1.1.md)。本机便携 Go 位于临时目录，未修改系统 PATH；后续终端需要显式使用该路径或自行配置已安装的 Go。

token 的结构、校验顺序、客户端存放要求与"为何不再需要 CSRF"见 [TOKEN.md](TOKEN.md)；第 3 课 Cookie/CSRF 方案的当时验收记录保留在 [VERIFY-T2.1.md](VERIFY-T2.1.md)，不代表当前实现。

## 显式数据库迁移

先迁移再执行账号初始化，两者均为独立命令，不随 HTTP 启动执行。

先准备一个 MySQL 8.0 空数据库，通过运行环境提供 DB_HOST、DB_PORT、DB_NAME、DB_USER、DB_PASSWORD（独立迁移命令不需要 TOKEN_SECRET）。迁移账号需具备该库建表、索引及外键权限。不得将密码放在命令参数、SQL 文件或 Git 中。

```powershell
go run ./cmd/migrate up
go run ./cmd/migrate up
```

第一次输出 `migrations applied`，第二次输出 `no change`。SQL 在 migrations 中并嵌入命令；没有 ORM、业务 seed 或 HTTP 启动自动建表。schema_migrations 保存版本/dirty 状态。MySQL DDL 不是整体事务：失败时检查 dirty 和实际表状态，不自动 force。已提供 down SQL 以明确逆迁移，但 CLI 仅开放 up，避免误删数据；本轮未验证 down。

000001 建立 classes/users/sessions/materials/knowledge_entries 与迁移元数据表；000002 `drop_sessions` 删除已废弃的会话表，因此当前是四张业务表加一张迁移元数据表。materials、knowledge_entries 均有非空 class_id 和独立索引；知识库复合外键确保班级与材料一致。password_hash 的 CHECK 只验证编码形式；cmd/seed 计算真实 scrypt 哈希，登录接口执行校验。

## 显式账号初始化（T3.1）

迁移后，通过进程环境提供 DB_* 及三个 SEED_*_PASSWORD（见 CONFIGURATION.md），在 backend 执行 `go run ./cmd/seed`，不需 TOKEN_SECRET。不自动加载 .env，没有默认密码，不要将真实值写入命令参数或源码。

首次输出 `accounts initialized`，创建 A/B 和 teacher_a（教师/A）、student_a1（学生/A）、student_b1（学生/B）。串行重复执行输出 `no change`，保留 ID、哈希及归属，即使提供不同密码也不重置。三个有效密码必须每次都提供，全部在数据库访问前校验；错误仅列变量名，非零退出。

班级按名称查询，ID 由数据库分配；班级重名或账号角色/班级冲突时拒绝，不覆盖。命令不自动迁移、不创建会话或材料，不提供并发初始化和中断补偿。随后可按 AUTH.md 登录。真实验证见 [VERIFY-T3.1.md](VERIFY-T3.1.md)。

## 材料样本及初始化（T3.2）

backend/fixtures 包含 class-a-intro.txt（CLASS_A_TXT_ONLY）、class-a-notes.md（# A notes / CLASS_A_MD_ONLY）和 class-b-seed.txt（CLASS_B_SEED_ONLY）。两个 A 班文件只待教师后续上传，初始化不写入 A 班材料。样本嵌入二进制并固定为 UTF-8/LF。

迁移、账号初始化后，提供 DB_* 和 UPLOAD_DIR（绝对路径；未来容器为 /data/uploads），在 backend 执行 `go run ./cmd/seed-materials`。首次输出 `material seed initialized`，在指定目录独占创建 seed-class-b-v1.txt，写入 B 班 source=seed、uploader_id=NULL 的材料及一条知识库记录；正文等于样本原文。

正常串行重复执行输出 `no change`，核对且保留原文件、ID、时间和文本。已有记录不完整、文件缺失/不同或无记录的同名文件均报错，不覆盖或自动修复。失败/中断可能留下文件，需人工核实；不提供种子中断补偿或并发初始化。没有教师 B，也未增加学生上传例外。真实验证见 [VERIFY-T3.2.md](VERIFY-T3.2.md)。

## 迁移隔离验收

可重复运行的隔离验收（不会访问机器上既有的 MySQL 服务）：

```powershell
./scripts/verify-schema.ps1 -MySQLBin '<MySQL 8.0 bin 绝对目录>' -GoCommand '<go 可执行文件绝对路径>'
```

脚本创建随机临时数据目录/端口，仅绑定 localhost，以一次性测试凭据运行两次迁移和数据库约束测试，结束后关闭实例并恢复环境变量。临时目录保留，不创建产品种子。普通 `go test ./...` 会跳过需要 MySQL 的集成测试；仅构建成功不能算数据库验收通过。实际命令、输出及限制见 [VERIFY-T1.2.md](VERIFY-T1.2.md)。
