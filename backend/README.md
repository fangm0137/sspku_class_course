# Backend — T1.1 / T1.2

当前实现 Go 服务骨架、必需配置校验、MySQL 延迟连接池、匿名 `GET /health` 和独立 SQL 迁移命令。尚未实现业务 API、会话业务或文件存储。无需运行 MySQL 即可验证进程存活；迁移必须连接真实 MySQL，这不代表业务已就绪。

模块当前最低要求 Go 1.26.0（新增依赖要求）；本轮使用 Go 1.27.1 验证，MySQL 驱动固定为 v1.10.1，golang-migrate 为 v4.20.1。在本目录执行：

```powershell
go mod download
go test ./...
go build ./...
```

必需环境变量：`SESSION_SECRET`、`DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`。缺失、空值或纯空白时进程非零退出，仅报告缺失变量名。`DB_PORT` 范围为 1–65535。真实凭据由运行环境提供，不写入源码或提交到版本库；当前不会自动加载 `.env`。

可选 `HTTP_ADDR` 默认为 `127.0.0.1:8080`。配置好环境后，在本目录运行 `go run ./cmd/server`，在另一个终端请求：

```powershell
curl.exe -i http://127.0.0.1:8080/health
```

预期 HTTP 200、Content-Type 为 application/json、正文 `{"status":"ok"}`。该接口不访问数据库、文件或会话。按 Ctrl+C 停止前台服务。完整配置示例与忽略规则属于 T1.3，Compose 属于第 7 组任务。

本轮实际验证记录见 [VERIFY-T1.1.md](VERIFY-T1.1.md)。本机便携 Go 位于临时目录，未修改系统 PATH；后续终端需要显式使用该路径或自行配置已安装的 Go。

## 显式数据库迁移

先准备一个 MySQL 8.0 空数据库，通过运行环境提供 DB_HOST、DB_PORT、DB_NAME、DB_USER、DB_PASSWORD（独立迁移命令不需要 SESSION_SECRET）。迁移账号需具备该库建表、索引及外键权限。不得将密码放在命令参数、SQL 文件或 Git 中。

```powershell
go run ./cmd/migrate up
go run ./cmd/migrate up
```

第一次输出 `migrations applied`，第二次输出 `no change`。SQL 在 migrations 中并嵌入命令；没有 ORM、业务 seed 或 HTTP 启动自动建表。schema_migrations 保存版本/dirty 状态。MySQL DDL 不是整体事务：失败时检查 dirty 和实际表状态，不自动 force。已提供 down SQL 以明确逆迁移，但 CLI 仅开放 up，避免误删数据；本轮未验证 down。

共五张业务表与一张迁移元数据表。materials、knowledge_entries 均有非空 class_id 和独立索引；知识库复合外键确保班级与材料一致。password_hash 的 CHECK 只验证编码形式，真实密码哈希计算由后续业务实现；T1.2 测试使用随机输入计算 scrypt 验证存储。

可重复运行的隔离验收（不会访问机器上既有的 MySQL 服务）：

```powershell
./scripts/verify-schema.ps1 -MySQLBin '<MySQL 8.0 bin 绝对目录>' -GoCommand '<go 可执行文件绝对路径>'
```

脚本创建随机临时数据目录/端口，仅绑定 localhost，以一次性测试凭据运行两次迁移和数据库约束测试，结束后关闭实例并恢复环境变量。临时目录保留，不创建产品种子。普通 `go test ./...` 会跳过需要 MySQL 的集成测试；仅构建成功不能算数据库验收通过。实际命令、输出及限制见 [VERIFY-T1.2.md](VERIFY-T1.2.md)。
