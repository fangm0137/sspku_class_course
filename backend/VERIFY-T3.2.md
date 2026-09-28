# T3.2 实际验证记录

日期：2026-09-26。仅交付三个样本与 B 班文件/材料/知识库初始化，不实现上传、读取 API、教师 B、学生上传例外或 T3.3 完整验收指南。

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

真实输出（空白已整理）：

```text
?   campusclaw/backend/cmd/migrate        [no test files]
ok  campusclaw/backend/cmd/seed           1.837s
ok  campusclaw/backend/cmd/seed-materials  0.801s
ok  campusclaw/backend/cmd/server         1.377s
?   campusclaw/backend/fixtures           [no test files]
ok  campusclaw/backend/internal/config    0.447s
?   campusclaw/backend/internal/db        [no test files]
ok  campusclaw/backend/internal/httpapi   2.159s
ok  campusclaw/backend/internal/schema    1.073s
ok  campusclaw/backend/internal/seed      0.608s
?   campusclaw/backend/migrations         [no test files]
```

build、vet 无输出、退出码 0。普通测试跳过 MySQL 集成项，TestSamplesAndConfiguration 验证样本标记互不混入、UTF-8/LF、Markdown 标题、缺失/相对目录配置拒绝。新增文件经 gofmt 格式化。

仓库根执行 `git check-ignore backend/fixtures/class-a-intro.txt backend/fixtures/class-a-notes.md backend/fixtures/class-b-seed.txt`，无输出，退出码 1（三个路径均未被忽略）。未执行 git add/commit。

## 独立 MySQL 与原文件验证

在 backend、上述 Go 环境下执行：

```powershell
./scripts/verify-schema.ps1 -Suite materials -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
```

脚本创建独立 MySQL 实例；测试调用账号初始化作为 fixture，再以子进程执行 seed-materials 的真实 main 入口。原文件写入测试临时目录，未操作用户运行数据。输出摘录（省略本机路径、重复 RUN 行及源码行号）：

```text
mysqld.exe Ver 8.0.46 for Win64 on x86_64 (MySQL Community Server - GPL)
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./cmd/seed-materials -run TestMySQL8MaterialSeed -v -count=1
MySQL 8.0.46
seed-materials: material seed initialized
seed-materials: no change
seed-materials: no change
PASS: distinct A TXT/MD samples; B seed original and text match; repeated initialization preserves IDs, timestamps and file; no extra teacher
--- PASS: TestMySQL8MaterialSeed (0.64s)
    --- PASS: TestMySQL8MaterialSeed/missing-class-no-file-or-record (0.01s)
    --- PASS: TestMySQL8MaterialSeed/complete-B-seed-and-A-preservation (0.37s)
    --- PASS: TestMySQL8MaterialSeed/serial-repeat-preserves-records-and-file (0.04s)
    --- PASS: TestMySQL8MaterialSeed/inconsistent-state-not-overwritten (0.01s)
PASS
ok  campusclaw/backend/cmd/seed-materials 1.266s
PASS isolated MySQL 8.0 materials verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

## Verify 对应证据

| 要求 | 真实检查 |
| --- | --- |
| 三个可区分样本 | 单元测试逐项核对 CLASS_A_TXT_ONLY / CLASS_A_MD_ONLY / CLASS_B_SEED_ONLY，Markdown 保留 # A notes |
| B 班完整种子 | 查询两表关联、同一 B class_id、source=seed、uploader_id=NULL、正确名称/大小；原文件 SHA-256 等于嵌入样本，提取文本逐字相同 |
| 串行幂等 | 首次后连续两次运行 main 返回 no change；两表 ID、元数据、created_at、正文均保持不变，原文件修改时间不变、目录文件数不增 |
| A 班不被覆盖 | 手工建立独立 A 班已入库测试 fixture（不经尚未实现的上传 API），两表及原文件均不变；最终为一份 A fixture 加一份 B seed |
| 不越界扩展身份 | 用户数量仍为三、无新增会话；命令不创建教师 B 或 HTTP 上传入口 |
| 异常边界 | B 班不存在时不创建文件/材料；文件冲突保留原内容并报错、文件缺失不自动修复 |

未测试种子并发、进程中断补偿、事务提交确认丢失或自动修复，这些不是本项完成依据；SQL 失败可能留下待人工核实的文件。未执行浏览器/Nginx/Compose 或读取隔离矩阵。数据库实例已关闭，Go 测试自动清理其临时文件目录；数据库临时目录按隔离脚本约定保留。
