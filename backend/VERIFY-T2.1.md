# T2.1 实际验证记录

日期：2026-09-26。本轮仅实现 GET /api/csrf、服务端令牌绑定与写请求校验组件；登录身份通过独立数据库 fixture 提供，不实现 login/logout/me、角色授权、材料或产品种子。

## 常规验证

在 backend 执行，使用已安装于临时目录的 Go 1.27.1：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
& $goTaskExe test ./... -count=1
& $goTaskExe build ./...
& $goTaskExe vet ./...
```

最终逻辑版本的实际测试输出：

```text
?   campusclaw/backend/cmd/migrate       [no test files]
ok  campusclaw/backend/cmd/server        0.771s
ok  campusclaw/backend/internal/config   0.393s
?   campusclaw/backend/internal/db       [no test files]
ok  campusclaw/backend/internal/httpapi  1.334s
ok  campusclaw/backend/internal/schema   1.022s
?   campusclaw/backend/migrations        [no test files]
```

build、vet 均退出码 0，无输出。普通测试跳过需要 TEST_MYSQL8 的数据库测试，以下为独立真实数据库执行的证据。gofmt 的临时替换曾被沙箱拒绝，随后通过工具批准完成格式化。

## 真实 MySQL 8.0

在 backend 执行：

```powershell
./scripts/verify-schema.ps1 -Suite csrf -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
```

脚本创建独立临时实例、随机数据库与一次性环境密码，先执行显式迁移再执行测试；未连接既有数据库，未启动 Compose。最终版本实际输出（省略本机可执行文件路径）：

```text
mysqld.exe Ver 8.0.46 for Win64 on x86_64 (MySQL Community Server - GPL)
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./internal/httpapi -run TestMySQL8CSRF -v -count=1
=== RUN   TestMySQL8CSRF
    csrf_integration_test.go:43: MySQL 8.0.46
=== RUN   TestMySQL8CSRF/anonymous-cookie-digests-and-no-store
=== RUN   TestMySQL8CSRF/rotation-boundaries-and-invalid-input
=== RUN   TestMySQL8CSRF/logged-in-token-expiry-and-session-cap
=== RUN   TestMySQL8CSRF/https-cookie-and-production-route
=== RUN   TestMySQL8CSRF/database-read-and-write-failures
--- PASS: TestMySQL8CSRF (0.24s)
    --- PASS: TestMySQL8CSRF/anonymous-cookie-digests-and-no-store (0.07s)
    --- PASS: TestMySQL8CSRF/rotation-boundaries-and-invalid-input (0.01s)
    --- PASS: TestMySQL8CSRF/logged-in-token-expiry-and-session-cap (0.03s)
    --- PASS: TestMySQL8CSRF/https-cookie-and-production-route (0.00s)
    --- PASS: TestMySQL8CSRF/database-read-and-write-failures (0.05s)
PASS
ok  campusclaw/backend/internal/httpapi 1.069s
PASS isolated MySQL 8.0 csrf verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

## 验收映射与边界

| 本项 verify | 实际证据 |
| --- | --- |
| 匿名获取与 no-store | anonymous-cookie-digests-and-no-store：200、唯一 JSON 字段、32 字节编码、匿名 user_id、摘要、Cookie 属性、无 CORS 许可头 |
| 30 分钟和会话到期 | rotation-boundaries-and-invalid-input、logged-in-token-expiry-and-session-cap：截止前 1 微秒通过，到期时拒绝；短会话限制令牌期限，轮换不续期 |
| 重复获取、绑定 | 旧 token 403、新 token 通过，另一会话 token 不能混用；过期 Cookie 获取时换匿名 ID |
| 错误令牌 403 / 匿名 401 | 独立测试 handler：错误、缺失、非法编码 token 拒绝；匿名、缺失、非法和到期 Cookie 不能通过登录身份门禁；拒绝不调用 handler |
| 已登录 fixture | 随机密码计算 scrypt 后插入独立用户，直接插入登录会话；有效登录会话获取 token 不改 Cookie，已删除会话失效 |
| 查库/写库错误 503 | 关闭连接池模拟无法查询；测试专用 INSERT/UPDATE trigger 注入失败，返回安全 JSON，失败不发新 Cookie，失败轮换保留旧 token；health 仍 200 |
| 同源检查 | TestSameOrigin：协议、端口、主机、默认端口、缺失/null/重复/非法 Origin、Sec-Fetch-Site、伪造转发头；TestCrossOriginBootstrapDoesNotUseDatabase 在 nil 数据库下安全返回 403 |
| 实际路由与 HTTPS 属性 | https-cookie-and-production-route 经 NewHandler 的 GET /api/csrf 检查 200 和 Secure Cookie；使用 httptest HTTPS 请求，未启动公网 TLS 服务 |

测试不输出生成的密码、Cookie、token 或密钥。会话替换登录流程、完整材料访问门禁、Nginx 转发、浏览器端到端和容器重建均属于后续任务，未宣称已验证。数据库提交时断连的故障注入不在本项测试覆盖内；代码提交错误分支统一返回 503、不发成功响应。

规约校验命令：`openspec validate add-auth-rbac-class-knowledge --strict`。

```text
Change 'add-auth-rbac-class-knowledge' is valid
```

`git diff --check` 退出码 0，仅有 LF/CRLF 转换提示。原有未提交修改保留，不自动提交 Git。
