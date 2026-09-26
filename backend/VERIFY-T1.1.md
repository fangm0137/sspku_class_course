# T1.1 验证记录

日期：2026-09-26。范围：Go 骨架、必需环境配置、MySQL 驱动声明和延迟连接池、匿名进程存活接口。仅此 task 完成，不代表认证、数据表、材料接口或部署完成。

## 环境与命令

Windows amd64，实际工具链输出：`go version go1.27.1 windows/amd64`。系统原先未配置 Go；从 Go 官方下载便携归档并按官方元数据核对 SHA-256 后解压至临时目录，没有修改系统 PATH。

以下为本轮实际执行的命令（工作目录为 backend；各命令退出码均为 0）：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = Join-Path $env:TEMP 'campusclaw-go1.27.1/go/bin/go.exe'
& $goTaskExe mod tidy
& $goTaskExe test ./... -count=1
& $goTaskExe build ./...
& $goTaskExe vet ./...
```

测试实际输出：

```text
ok   campusclaw/backend/cmd/server       1.367s
ok   campusclaw/backend/internal/config  0.537s
?    campusclaw/backend/internal/db     [no test files]
ok   campusclaw/backend/internal/httpapi 0.943s
```

build 与 vet 成功且无输出。测试覆盖六项配置的空值/纯空白拒绝、缺失配置禁止开始监听、非法端口与错误脱敏、密码原值保留，以及无数据库/文件依赖的匿名 health。db 包没有独立测试，其延迟连接池构造通过 cmd/server 的不可达数据库测试执行。

## 编译后进程验证

实际执行 `go build -o <临时目录>/campusclaw-t11-smoke.exe ./cmd/server` 后，通过 PowerShell 的 ProcessStartInfo 启动隐藏测试进程。测试密钥和密码在内存随机生成，未写入文件或报告。依次从子进程环境移除六项必需变量，等待退出并检查错误只标识配置项，不包含生成的凭据；所有检查通过。

随后在随机 localhost 端口启动完整配置进程，DB_HOST=unreachable.invalid，通过 Invoke-WebRequest 请求 /health。实际输出：

```text
PASS missing SESSION_SECRET: exit=1, safe error
PASS missing DB_HOST: exit=1, safe error
PASS missing DB_PORT: exit=1, safe error
PASS missing DB_NAME: exit=1, safe error
PASS missing DB_USER: exit=1, safe error
PASS missing DB_PASSWORD: exit=1, safe error
PASS unreachable database: GET /health 200 {"status":"ok"}
```

验证结束已关闭测试进程。未启动 MySQL、Compose 或前端；没有执行数据库建表、会话、上传、Nginx 503 或持久化集成验收。

## 规约对应

- Credential and content safety / Missing secret：缺失环境配置时启动失败，不泄露凭据。
- Local deployment health and persistence / Anonymous health check：本轮验证 Go 进程自身的 200 JSON 行为及依赖隔离；Compose/Nginx 相关完整验收留在第 7 组。
- 不因上述局部验证勾选任何其他 task。
