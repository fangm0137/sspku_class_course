# T1.3 实际验证记录

日期：2026-09-26。仅配置示例、忽略规则和说明；不实现认证、种子或部署。根 README.md 在本轮开始前已有用户修改，本轮未修改它。

## 配置逐项核对

对照 internal/config/config.go：SESSION_SECRET、DB_HOST、DB_PORT、DB_NAME、DB_USER、DB_PASSWORD 六个必需项，以及可选 HTTP_ADDR，均在根 .env.example 中。三个 SEED_*_PASSWORD 变量明确标注为后续读取，不宣称当前生效。

对照 design：mysql_data → /var/lib/mysql；material_data → /data，含 uploads/staging；登录 8 小时、匿名 30 分钟、CSRF 不超过 30 分钟或会话剩余时间；三名账号各有独立密码变量。示例与 CONFIGURATION.md 均已覆盖，不增加运行时开关。

在仓库根执行：

```powershell
$assignments = Get-Content .env.example | Where-Object { $_ -match '^[A-Z][A-Z0-9_]*=' }
if ($assignments.Count -ne 10 -or ($assignments | Where-Object { $_ -notmatch '=$' })) { throw 'Example invalid' }
'PASS: 10 variables, all values empty'
$tracked = git ls-files
$bad = $tracked | Where-Object { $_ -match '(^|/)\.env($|\.)' -and $_ -notmatch '(^|/)\.env\.example$' }
if ($bad) { throw 'Tracked env file detected' }
'PASS: no tracked real .env files'
```

实际输出：

```text
PASS: 10 variables, all values empty
PASS: no tracked real .env files
```

## 忽略规则

实际命令：

```powershell
git check-ignore -v --no-index .env .env.local backend/.env backend/.env.production secrets/session.key backend/data/mysql.dat frontend/dist/index.html backend/logs/server.log .env.example backend/migrations/000001_initial_schema.up.sql
```

实际输出（空白分隔已排版）：

```text
.gitignore:8:.env                 .env
.gitignore:9:.env.*               .env.local
.gitignore:8:.env                 backend/.env
.gitignore:9:.env.*               backend/.env.production
.gitignore:13:secrets/            secrets/session.key
.gitignore:16:data/               backend/data/mysql.dat
.gitignore:23:dist/               frontend/dist/index.html
.gitignore:19:*.log               backend/logs/server.log
.gitignore:10:!.env.example       .env.example
```

`!` 表示示例保留；SQL 路径没有排除命中。上述为路径探针，不创建真实凭据。

Docker 规则使用临时独立 Go module 的 github.com/moby/patternmatcher v0.6.0 验证，没有增加项目依赖。工具位于本机 `$env:TEMP/campusclaw-context-verify`，使用 ignorefile.ReadAll 读取实际 .dockerignore，逐项调用 MatchesOrParentMatches，断言 15 个探针排除，并遍历 backend 检查所有 .go/.sql/go.mod/go.sum 保留。探针涵盖根及嵌套 .env、示例、私钥、secrets、数据、上传、暂存、日志、node_modules、dist、可执行文件及 .git/config。

实际命令（临时目录内；Go 路径见下一节）：

```powershell
& "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe" run . E:/sspku-my-course-demo
```

实际输出：

```text
PASS: 15 sensitive/data/build paths excluded; 15 Go/module/migration files retained
```

初次下载匹配库受网络沙箱限制，随后经工具批准下载成功。这是构建上下文规则验证，不是镜像构建；本轮没有 Dockerfile、前端构建或 Compose 执行。

## 当前日志错误路径与规约校验

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
& "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe" test ./internal/config ./cmd/server ./cmd/migrate -count=1
```

实际输出：

```text
ok  campusclaw/backend/internal/config  0.439s
ok  campusclaw/backend/cmd/server       0.715s
?   campusclaw/backend/cmd/migrate      [no test files]
```

现有测试断言缺配置、无效端口错误不回显测试凭据/输入值，启动失败不开始监听；人工核对 server 仅记录安全错误，migrate 仅打印安全错误/固定状态。未用真实凭据执行测试，未宣称尚未实现的业务日志已通过验收。

在仓库根执行 `openspec validate add-auth-rbac-class-knowledge --strict`，实际输出：

```text
Change 'add-auth-rbac-class-knowledge' is valid
```

`git diff --check` 退出码 0，仅报告仓库 LF/CRLF 转换提示，无空白错误。凭据隐藏在任意普通源码中的情形不能仅靠 ignore 规则排除；本轮未作全面密钥审计或镜像扫描。
