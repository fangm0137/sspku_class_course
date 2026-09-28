# T5.1–T5.4 真实验证记录

日期：2026-09-27。用户授权一次完成第 5 组，分别验收后勾选。Go 1.27.1、MySQL 8.0.46；使用随机临时库及文件目录，不修改既有数据库，不启动 Compose，不提交 Git。

## 实际命令

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
& $goTaskExe test ./internal/httpapi -run TestUploadInput -v -count=1
./scripts/verify-schema.ps1 -Suite upload -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand $goTaskExe
& $goTaskExe test ./... -count=1
& $goTaskExe build ./...
& $goTaskExe vet ./...
```

TestUploadInput 通过 txt/md 样本、恰好 5 MiB、超限、BOM、空白/空/BOM-only、非法 UTF-8、扩展名、长文件名和 Windows/Unix 路径片段案例；缺文件、错误字段、多文件、损坏 multipart、整体超限与尾随字节也在该测试断言中。最后两项修正后通过完整 go test ./... 重新验证。

最终 MySQL 命令输出摘录（所有列出的子测试均实际执行）：

```text
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./internal/httpapi -run TestMySQL8Upload -v -count=1
=== RUN   TestMySQL8Upload
    upload_integration_test.go:49: MySQL 8.0.46
--- PASS: TestMySQL8Upload (1.09s)
    --- PASS: TestMySQL8Upload/T5.1-T5.2-valid-samples-and-isolation (0.03s)
    --- PASS: TestMySQL8Upload/T5.2-rejections-have-no-effects (0.01s)
    --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback (0.05s)
        --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback/file (0.00s)
        --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback/file_written (0.00s)
        --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback/extract (0.01s)
        --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback/material (0.01s)
        --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback/knowledge (0.01s)
        --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback/move (0.01s)
        --- PASS: TestMySQL8Upload/T5.3-determined-failures-rollback/before_commit (0.01s)
    --- PASS: TestMySQL8Upload/T5.3-real-database-write-errors (0.04s)
    --- PASS: TestMySQL8Upload/T5.1-full-size-and-BOM-persisted (0.26s)
    --- PASS: TestMySQL8Upload/T5.3-compensation-and-recovery-failure (0.01s)
    --- PASS: TestMySQL8Upload/T5.3-unknown-commit-preserves-then-reconciles (0.03s)
    --- PASS: TestMySQL8Upload/T5.4-response-loss-and-independent-retry (0.02s)
    --- PASS: TestMySQL8Upload/T5.3-recovery-waits-for-unresolved-transaction (0.11s)
PASS
ok  campusclaw/backend/internal/httpapi 1.935s
PASS isolated MySQL 8.0 upload verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

完整 test ./... 退出 0：cmd/seed、cmd/seed-materials、cmd/server、internal/config、internal/httpapi、internal/schema、internal/seed 均为 ok，其余包为 no test files；build 和 vet 无输出、退出 0。普通测试跳过 MySQL 集成项，以上独立套件才是本轮数据库证据。

## 逐项证据与限制

- T5.1：输入边界单元测试及真实 5 MiB/BOM 入库下载；安全展示名与随机存储键断言。没有将客户端路径用于写文件。
- T5.2：三个产品账号真实登录并获取新 token；教师携带伪造 class_id 仍写 A 班，source/uploader/text/原文件正确，同班立即列表/详情/下载成功，B 班不可读。学生/匿名/错误 CSRF 使用禁止读取正文的替身，拒绝后两表及文件数量不变。
- T5.3：注入写入前后、提取、两表、移动、确定提交前失败，验证回滚与文件补偿；真实 MySQL BEFORE INSERT 触发器分别拒绝两表写入，得到安全 500 且无半成品。补偿失败只尝试一次，业务 503、health 200；清理再次失败不就绪，人工恢复调用后清理成功。
- T5.3 未知提交：测试钩子分别在真实 Commit/真实 Rollback 后返回确认丢失错误，响应 503 且文件保留，恢复后按真实数据库结果保留或删除。另用尚未提交的真实 InnoDB 事务验证恢复锁定查询等待，超时不删文件，提交后可恢复。没有实际切断 TCP 或断电，未知提交由应用边界注入模拟。
- T5.4：ResponseWriter 写入失败模拟客户端断开；数据库及原文件仍在。独立重试获得新 ID，两份附件均完整。未宣称经过浏览器/Nginx 或真实网络中断验收。

实现：internal/httpapi/upload_input.go、upload.go、upload_recovery.go；路由及启动接入：handler.go、cmd/server/main.go；测试：upload_input_test.go、upload_integration_test.go；复现入口：scripts/verify-schema.ps1 -Suite upload。功能说明见 UPLOAD.md。

未实现第 6 组 React 页面、第 7 组 Compose/Nginx、第 8 组代理端到端与重建验收；没有新增并发初始化、持续自动重试、分页、配额、去重或非目标业务。
