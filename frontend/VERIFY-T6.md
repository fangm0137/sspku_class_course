# T6.1–T6.2 验证记录

2026-09-27。用户授权完成前端组，两项均在实际验证后勾选。环境：Node.js 24.21.0、React 18.3.1、TypeScript 5.9.3、Vite 8.3.1、Playwright 1.63.0、Edge/Chromium 154.0.4258.37、Go 1.27.1、MySQL 8.0.46。

## 构建与凭据检查

在 frontend 执行 `node --check tests/browser.mjs` 和 `npm run build`，均退出 0。最终构建前，在进程环境中给 SESSION_SECRET、DB_PASSWORD 与三个 SEED_*_PASSWORD 提供随机测试标记，构建后恢复原环境；标记不打印、不保存。实际执行的 PowerShell：

```powershell
$buildNames = @('SESSION_SECRET','DB_PASSWORD','SEED_TEACHER_A_PASSWORD','SEED_STUDENT_A1_PASSWORD','SEED_STUDENT_B1_PASSWORD')
$previousBuildEnv = @{}
try {
    foreach ($name in $buildNames) {
        $previousBuildEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, [guid]::NewGuid().ToString('N'), 'Process')
    }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'build verification failed' }
} finally {
    foreach ($name in $buildNames) { [Environment]::SetEnvironmentVariable($name, $previousBuildEnv[$name], 'Process') }
}
```

实际输出：

```text
> tsc --noEmit && vite build && node scripts/check-dist.mjs
vite v8.3.1 building client environment for production...
✓ 16 modules transformed.
dist/index.html                   0.45 kB │ gzip:  0.32 kB
dist/assets/index-BI94Elzu.css    9.75 kB │ gzip:  2.91 kB
dist/assets/index-W1T89Vki.js   151.98 kB │ gzip: 49.91 kB
✓ built in 98ms
PASS build contains no server credential names or supplied values
```

构建扫描覆盖服务端敏感变量名与本次提供的随机值；配置不加载环境文件，客户端仅相对 API 路径。不宣称静态扫描能识别任意未知凭据。依赖版本锁定在 package-lock.json；npm 安装输出 0 vulnerabilities，未据此声称无所有安全风险。

## 真实浏览器、Go 与 MySQL

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
./scripts/verify-schema.ps1 -Suite frontend -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
```

最终输出（时区修正后）：

```text
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./internal/httpapi -run TestMySQL8Frontend -v -count=1 -timeout 180s
=== RUN   TestMySQL8Frontend
    frontend_integration_test.go:38: MySQL 8.0.46
    frontend_integration_test.go:74: Browser 154.0.4258.37
        PASS T6.1 login, token rotation, old-token 403, HTTP Cookie, refresh and no Web Storage
        PASS T6.2 teacher TXT/MD upload, same-class refresh/read/download, both-way isolation and student controls
        PASS T6.2 escaped script text, attachment bytes and mobile layout
        PASS T6.1 injected 503 keeps identity, failed logout stays signed in, initial unknown state and recovery
        PASS T6.1 logout clears Cookie, old Cookie 401, UI clears materials on confirmed session loss
        PASS T6.1 direct HTTPS Secure Cookie and no browser runtime errors
        PASS all frontend browser checks
--- PASS: TestMySQL8Frontend (5.88s)
PASS
ok  campusclaw/backend/internal/httpapi 6.831s
PASS isolated MySQL 8.0 frontend verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

测试入口为 backend/internal/httpapi/frontend_integration_test.go 和 frontend/tests/browser.mjs。临时实例建立真实三账号及 B 班种子，Go 启动 HTTP/TLS 测试服务，Vite 同源代理到 Go，浏览器实际操作页面。秘密仅在测试进程内传递；关闭所有测试服务，保留不含凭据的页面截图，不保留 trace、登录录像或会话快照。

### T6.1

三个身份通过表单真实登录；登录前后 token 不同，旧 token 提交实际登出接口返回 403；刷新页面恢复身份。HTTP Cookie 为 HttpOnly、SameSite=Lax、Path=/、不透明 ID；直接 TLS 测试服务的登录 Cookie 另有 Secure 和 Max-Age=28800。浏览器不能从 document.cookie 读取会话，localStorage/sessionStorage 为空。成功登出后 Cookie 清除，原 Cookie 重放 401；外部吊销后页面刷新回到登录并清除材料。

浏览器拦截 /api/me、/api/logout 注入 503，验证已知身份保留、退出失败不误报成功；无已知身份的首次加载显示未知状态和重新连接。恢复响应后可以重试。该 503 验证是页面响应注入，没有在此测试中关闭 MySQL。

### T6.2

教师通过文件选择控件分别上传 TXT/MD，接口返回 material_id，页面立即刷新 items 并显示 text；A1 刷新可见，B1 不可见，两学生无上传控件。真实请求核对双向跨班 404，学生伪造上传仍 403。下载事件得到的文件名与原始字节一致。含 script/img 样式的文本作为文字呈现，无子元素注入和脚本执行。

桌面 1440×960 与手机 390×844 布局已截图并目视检查，手机无水平溢出：test-results/login.png、teacher.png、student-mobile.png（被 Git 忽略）。测试发现 MySQL 默认时区导致材料时间偏移，已先补 design 再将连接 session time_zone 设为 +00:00；浏览器断言新材料 created_at 与实际当前时刻差小于一分钟。未改写历史数据。

## 后端回归与范围

时区修正后在 backend 使用相同 Go 路径执行 `go test ./... -count=1`、`go build ./...`、`go vet ./...`，均退出 0，build/vet 无输出。普通测试中的真实 MySQL 项默认跳过，不能代替上述 frontend 隔离套件。

本轮没有启动 Compose、部署 Nginx、提交 Git，也未执行第 8 组容器重建/完整代理验收。页面演示及本地启动见 README.md；第 7、8 组仍未勾选。
