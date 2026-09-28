# T3.1 实际验证记录

日期：2026-09-26。仅账号及班级初始化，未创建材料样本/知识库种子，未完成 T3.2/T3.3。

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
& $goTaskExe test ./... -count=1
& $goTaskExe build ./...
& $goTaskExe vet ./...
```

真实输出（空白已整理）：

```text
?   campusclaw/backend/cmd/migrate       [no test files]
ok  campusclaw/backend/cmd/seed          1.256s
ok  campusclaw/backend/cmd/server        0.886s
ok  campusclaw/backend/internal/config   0.477s
?   campusclaw/backend/internal/db       [no test files]
ok  campusclaw/backend/internal/httpapi  2.013s
ok  campusclaw/backend/internal/schema   1.436s
ok  campusclaw/backend/internal/seed     1.095s
?   campusclaw/backend/migrations        [no test files]
```

build/vet 无输出，退出码 0。此轮随后将集成测试的成功初始化调用改为子进程运行真实 main 入口，再执行以下数据库验证。普通测试跳过 TEST_MYSQL8 集成项。格式化受沙箱限制后经工具批准完成。

```powershell
./scripts/verify-schema.ps1 -Suite seed -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
```

脚本使用独立临时 MySQL，先两次迁移；测试启动测试二进制子进程调用 seed 的真实 main，三个密码只通过子进程环境传递，均为运行时随机值。缺密码/冲突检查调用同一 run 入口。实际输出摘录（省略本机路径及测试源码行号）：

```text
mysqld.exe Ver 8.0.46 for Win64 on x86_64 (MySQL Community Server - GPL)
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./cmd/seed -run TestMySQL8Seed -v -count=1
MySQL 8.0.46
seed: accounts initialized
seed: no change
seed: no change
PASS: 2 classes, 3 accounts; repeated initialization preserves IDs and hashes; original passwords still authenticate
--- PASS: TestMySQL8Seed (1.20s)
    --- PASS: TestMySQL8Seed/missing-passwords-no-writes (0.00s)
    --- PASS: TestMySQL8Seed/first-initialization (0.65s)
    --- PASS: TestMySQL8Seed/serial-repeat-preserves-ids-and-hashes (0.05s)
    --- PASS: TestMySQL8Seed/seed-passwords-work-through-login (0.48s)
    --- PASS: TestMySQL8Seed/conflicts-do-not-overwrite (0.02s)
PASS
ok  campusclaw/backend/cmd/seed 2.151s
PASS isolated MySQL 8.0 seed verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

覆盖：逐个缺失密码时 classes/users 均为零；初始化得到两班三账号及准确角色/归属，sessions/materials/knowledge_entries 在初始化后均为空。验证 scrypt 格式、每个账号独立盐、无明文；串行重复和更换环境密码重复均逐字段比对账号 ID/归属/哈希不变。通过真实 CSRF/login handler 验证三个原密码仍可登录，新提供的密码被拒绝。重名班级和冲突账号安全拒绝、不覆盖。单元测试另覆盖空白、非法 UTF-8、129 码点，以及 1/128 码点和含首尾空格有效密码保留原值。

未在用户的运行数据库执行初始化，未写真实密码；数据库测试实例已关闭。没有测试并发初始化、进程中断补偿、材料种子、容器或前端，未启动 Compose。普通事务回滚不代表实现了非目标中的初始化中断补偿。
