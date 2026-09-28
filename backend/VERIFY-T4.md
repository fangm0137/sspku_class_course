# T4.1–T4.3 真实验收记录

2026-09-27，本轮经用户授权合并完成三个关联任务，分别验收后勾选。未启动 Compose，未修改既有数据库，未提交 Git。Go 1.27.1，独立 MySQL 8.0.46。

## 命令与结果

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = Join-Path $env:TEMP 'campusclaw-go1.27.1/go/bin/go.exe'
& $goTaskExe test ./... -count=1
& $goTaskExe build ./...
& $goTaskExe vet ./...
./scripts/verify-schema.ps1 -Suite access -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand $goTaskExe
```

前三条均退出 0：test 的 cmd/seed、cmd/seed-materials、cmd/server、internal/config、internal/httpapi、internal/schema、internal/seed 均为 ok，其余包无测试；build/vet 无输出。普通测试会跳过需真实 MySQL 的集成项，单独隔离验收输出如下：

```text
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./internal/httpapi -run TestMySQL8Access -v -count=1
=== RUN   TestMySQL8Access
    access_integration_test.go:32: MySQL 8.0.46
--- PASS: TestMySQL8Access (0.60s)
    --- PASS: TestMySQL8Access/T4.1-trusted-identity-role-and-no-side-effects (0.01s)
    --- PASS: TestMySQL8Access/T4.2-list-detail-file-and-response-fields (0.03s)
    --- PASS: TestMySQL8Access/T4.3-bidirectional-IDs-anonymous-and-bypass (0.01s)
    --- PASS: TestMySQL8Access/T4.2-missing-content-and-storage-containment (0.01s)
    --- PASS: TestMySQL8Access/T4.1-T4.2-database-failure-is-503 (0.04s)
PASS
ok  campusclaw/backend/internal/httpapi 1.456s
PASS isolated MySQL 8.0 access verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

上面的行号为格式化前实际输出，格式化后源码位置可能变化。脚本随机创建临时实例/数据库及运行凭据，结束关闭实例，未输出凭据。

## 任务与证据

| 任务 | 实际验证 |
| --- | --- |
| 4.1 | 两学生伪造 teacher 仍 403，无身份 401；教师伪造 student/class_id 仍取得真实身份；数据库角色修改立即生效；拒绝不读取正文，三份文件和两类记录数量不变；数据库关闭时门禁 503 |
| 4.2 | 三账号真实登录后调用三个真实 GET 路由；A 两条/B 一条及空 items；列表四字段、详情五字段和 text、下载附件字节一致；同班 200、匿名 401、跨班/不存在同形 404；材料查询及身份查库故障 503；文件/知识库缺失和不安全存储键 500；DB 关闭 health 仍 200 |
| 4.3 | A/B 双向直接 ID 与下载路径，伪造 class_id/role 不越权；静态/知识库/fixture 绕过路径 404；无效 ID、伪造 Cookie 拒绝；错误严格两个安全字段；读取接口说明见 MATERIALS.md |

实现位于 internal/httpapi/materials.go、handler.go；回归位于 access_integration_test.go 与 materials_test.go，运行入口为 scripts/verify-schema.ps1 的 access 套件。配置及 server 接入 UPLOAD_DIR，示例与说明同步。

## 边界

A 班数据通过独立 SQL fixture 建立，B 班通过产品种子命令底层函数建立，不代表教师上传已完成。教师权限使用测试 handler，不开放上传接口。本轮未验证 HTTP 网络部署、浏览器、Nginx、容器重建、上传事务或生产并发；这些属于后续任务。未新增分页、存储配额或独立知识库 API。
