# T2.3 实际验证记录

日期：2026-09-26。仅完成 GET /api/me 与 POST /api/logout，以及三种认证接口与 CSRF 的调用说明；无产品种子、材料或部署实现。

## 常规检查

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
& $goTaskExe test ./... -count=1
& $goTaskExe build ./...
& $goTaskExe vet ./...
```

实际测试输出（排版空白已整理）：

```text
?   campusclaw/backend/cmd/migrate       [no test files]
ok  campusclaw/backend/cmd/server        0.979s
ok  campusclaw/backend/internal/config   0.451s
?   campusclaw/backend/internal/db       [no test files]
ok  campusclaw/backend/internal/httpapi  1.800s
ok  campusclaw/backend/internal/schema   1.020s
?   campusclaw/backend/migrations        [no test files]
```

build、vet 无输出，退出码均为 0。普通测试跳过 MySQL 集成项，以下单独执行真实数据库测试。文件格式化的普通写入被沙箱阻止，随后工具批准 gofmt，退出码 0；没有修改依赖。

## MySQL 集成检查

同一 backend 目录、上述 Go 环境下执行：

```powershell
./scripts/verify-schema.ps1 -Suite session -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
```

实际输出（省略 MySQL 可执行文件的本机绝对路径）：

```text
mysqld.exe Ver 8.0.46 for Win64 on x86_64 (MySQL Community Server - GPL)
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./internal/httpapi -run TestMySQL8Session -v -count=1
=== RUN   TestMySQL8Session
    session_integration_test.go:22: MySQL 8.0.46
=== RUN   TestMySQL8Session/teacher-student-me-latest-identity
=== RUN   TestMySQL8Session/anonymous-invalid-expired-priority
=== RUN   TestMySQL8Session/csrf-failures-preserve-session
=== RUN   TestMySQL8Session/logout-clears-cookie-and-rejects-replay
=== RUN   TestMySQL8Session/delete-failure-rollback-and-database-outage
--- PASS: TestMySQL8Session (1.09s)
    --- PASS: TestMySQL8Session/teacher-student-me-latest-identity (0.24s)
    --- PASS: TestMySQL8Session/anonymous-invalid-expired-priority (0.09s)
    --- PASS: TestMySQL8Session/csrf-failures-preserve-session (0.09s)
    --- PASS: TestMySQL8Session/logout-clears-cookie-and-rejects-replay (0.33s)
    --- PASS: TestMySQL8Session/delete-failure-rollback-and-database-outage (0.24s)
PASS
ok  campusclaw/backend/internal/httpapi 1.956s
PASS isolated MySQL 8.0 session verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

## Verify 对应证据

| 要求 | 实际验证 |
| --- | --- |
| me 四字段 | 经真实 csrf/login 路由获得教师/学生 Cookie，me 返回 id/username/role/class_id 且仅这四个字段；不信任查询参数；数据库修改班级后同一 Cookie 读取最新身份 |
| 登出 200、删除、清 Cookie | 返回仅 ok=true；查询 sessions 确认目标行消失；Cookie 名称/空值/Path/HttpOnly/Lax/过期/MaxAge 正确；其他同账号会话仍有效 |
| 旧 Cookie 重放 | 保存 Cookie 后登出，再调用 me/logout 均 401；无 Cookie、非法、未知、匿名及过期会话也 401 |
| CSRF 403 | 缺失、错误、轮换前、过期 token；缺失/跨源 Origin 均 403，无删除或清 Cookie；匿名身份 401 优先 |
| 数据库失败 503 | 测试专用 DELETE trigger 注入错误，503、不清 Cookie，原行及 me 仍有效；暂时重命名 users/sessions 使查询失败，返回安全 503；恢复后原 Cookie 能读取并登出；关闭连接池后两入口 503，health 200 |
| 接口说明 | AUTH.md 串联 csrf → login → 新 csrf → me → logout → 旧会话拒绝；LOGIN.md、CSRF.md、README 同步当前状态 |

测试仅使用隔离数据库和随机测试凭据，密码为带盐 scrypt 哈希，不打印密码、token 或 Cookie。HTTP/HTTPS 由 httptest 请求模拟，未启动真实 TLS 服务或验证 Nginx/浏览器；没有执行 Compose。提交确认丢失未做故障注入，代码仅在 Commit 成功后清 Cookie，错误统一 503。临时 MySQL 已关闭，原有未提交修改保留。
