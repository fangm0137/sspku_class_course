# T2.2 实际验证记录

日期：2026-09-26。仅实现登录与会话替换，不实现后续任务。环境为 Windows、Go 1.27.1、MySQL 8.0.46。

## 常规验证

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
& $goTaskExe test ./... -count=1
& $goTaskExe build ./...
& $goTaskExe vet ./...
```

真实输出（对齐空白已整理）：

```text
?   campusclaw/backend/cmd/migrate       [no test files]
ok  campusclaw/backend/cmd/server        1.010s
ok  campusclaw/backend/internal/config   0.447s
?   campusclaw/backend/internal/db       [no test files]
ok  campusclaw/backend/internal/httpapi  1.593s
ok  campusclaw/backend/internal/schema   0.758s
?   campusclaw/backend/migrations        [no test files]
```

build、vet 均无输出，退出码 0。普通测试跳过 MySQL 集成测试，数据库验证单独执行如下。gofmt 临时替换曾受沙箱限制，工具批准后完成格式化；没有依赖变更。

## 独立真实数据库

在 backend 执行（GOPATH/GOCACHE 同上）：

```powershell
./scripts/verify-schema.ps1 -Suite login -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
```

输出摘录，省略本机绝对路径和逐个 malformed 子场景的重复 PASS 行：

```text
mysqld.exe Ver 8.0.46 for Win64 on x86_64 (MySQL Community Server - GPL)
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./internal/httpapi -run TestMySQL8Login -v -count=1
--- PASS: TestMySQL8Login (1.35s)
    --- PASS: TestMySQL8Login/success-teacher-1 (0.20s)
    --- PASS: TestMySQL8Login/success-student-2 (0.14s)
    --- PASS: TestMySQL8Login/success-student-3 (0.14s)
    --- PASS: TestMySQL8Login/malformed-input-and-priority (0.01s)
    --- PASS: TestMySQL8Login/credentials-and-untrusted-fields (0.25s)
    --- PASS: TestMySQL8Login/concurrent-session-replacement (0.07s)
    --- PASS: TestMySQL8Login/session-expiry-eight-hours (0.07s)
    --- PASS: TestMySQL8Login/database-failures-and-rollback (0.26s)
PASS
ok  campusclaw/backend/internal/httpapi 2.126s
PASS isolated MySQL 8.0 login verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

## 覆盖关系

| 验收条件 | 证据 |
| --- | --- |
| 教师/学生与两班身份 | success 三组通过真实注册路由检查 200、user 的四个且仅四个字段，数据库真实用户/班级匹配 |
| 输入边界 | TestLoginInput 检查 Unicode 用户名 1/64、密码 1/128、零长度、65/129；真实登录 fixture 使用 64 码点教师名、1 码点学生名及 128/1 长度密码；空白密码允许、原字符串不裁剪 |
| 通用 400 | malformed-input-and-priority 覆盖 19 种错误输入及非 JSON Content-Type，无新登录行、无 Cookie；错误 CSRF 优先于输入错误 |
| 通用 401 | 不存在账号与错误密码响应逐字一致；密码前后空格不被裁剪；TestPasswordVerification 拒绝错误和损坏/非支持格式哈希 |
| 可信身份 | role/class_id 伪造值被忽略，成功返回真实学生和 A 班 |
| 8 小时及 Cookie | 查询新 sessions 行为 expires_at-created_at=8h、仅摘要可查、无旧 token；Cookie 32 字节不透明且 MaxAge=28800、HttpOnly/Lax/Path=/；到期前 1 微秒通过测试 gate，到期即 401 |
| 新旧会话替换 | 匿名和已登录旧会话均被删除，旧 Cookie/旧 token 不能登录，新 Cookie 需重新获取 CSRF；双并发登录只有一个 200、另一个 403，净新增一个登录会话 |
| 数据库失败 | 独立 INSERT/DELETE 触发器注入失败返回 503、回滚保留旧行；临时重命名 users 模拟登录查询错误；恢复后可登录；关闭连接池时 CSRF/登录入口为 503 |

隔离脚本生成一次性凭据，不连接既有库；测试用户密码由运行时随机输入派生为 scrypt 哈希后插入，不插入明文、不打印凭据。异常触发器只存在于独立测试库。测试结束停止实例，临时数据保留。没有产品种子、Compose、前端或材料 API 验证。

未做数据库提交确认丢失故障注入、实际浏览器或 Nginx 验收；代码在 Commit 错误时返回 503，不发送成功 Cookie。HTTPS Cookie 设置复用直接 TLS 判断，本轮登录集成通过 HTTP 执行，未宣称验证代理 TLS。

在仓库根执行 `openspec validate add-auth-rbac-class-knowledge --strict`：

```text
Change 'add-auth-rbac-class-knowledge' is valid
```
