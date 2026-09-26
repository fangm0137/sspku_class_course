# T1.2 真实验证记录

日期：2026-09-26。仅实现显式 schema 与迁移，不实现产品种子或业务逻辑。

## 修改与范围

- 五张业务表：classes、users、sessions、materials、knowledge_entries；另有 golang-migrate 的 schema_migrations 元数据。
- materials 和 knowledge_entries 的 class_id 均 NOT NULL 且有独立索引；knowledge_entries 的 material_id 唯一，复合外键保证材料/班级一致。
- sessions 保留匿名 user_id=NULL、会话 HMAC 摘要、到期与 CSRF 字段；materials.source 为 seed/upload。
- 当前 change 没有讲义/作业/助手/技能占位表的定义，故未新增这些表。
- 当前种子任务编号为 3.x，本轮没有实现；数据库测试 fixture 均在事务中插入并回滚，不属于产品初始化种子。

## 工具与真实命令

Go 1.27.1 windows/amd64，MySQL Community Server 8.0.46（官方便携包，校验与官网下载页 MD5 一致）。未使用机器上运行的 MySQL 9.1，也未启动 Compose。临时实例仅绑定 localhost 随机端口，数据目录随机且独立，密码在内存生成，不输出、不写入版本库。结束后已停止实例，保留临时目录供检查。

在 backend 执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
$goTaskExe = Join-Path $env:TEMP 'campusclaw-go1.27.1/go/bin/go.exe'
./scripts/verify-schema.ps1 -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand $goTaskExe
```

脚本通过环境参数调用两次 `go run ./cmd/migrate up`，然后执行 `go test ./internal/schema -run TestMySQL8Schema -v -count=1`。后者真正执行 SELECT VERSION、SHOW TABLES、SHOW CREATE TABLE、参数化插入和约束负例；不是 SQL 静态文本检查。

以下为该次验收原始输出（路径为本机临时工具链位置，无凭据）：

```text
C:\Users\24155\AppData\Local\Temp\campusclaw-mysql-8.0.46\mysql-8.0.46-winx64\bin\mysqld.exe  Ver 8.0.46 for Win64 on x86_64 (MySQL Community Server - GPL)
COMMAND: go run ./cmd/migrate up (first run, empty database)
migrations applied
COMMAND: go run ./cmd/migrate up (second run)
no change
COMMAND: go test ./internal/schema -run TestMySQL8Schema -v -count=1
=== RUN   TestMySQL8Schema
    schema_integration_test.go:45: SELECT VERSION(): 8.0.46
    schema_integration_test.go:54: repeat Up 1: no change
    schema_integration_test.go:54: repeat Up 2: no change
    schema_integration_test.go:76: SHOW TABLES: classes, knowledge_entries, materials, schema_migrations, sessions, users
    schema_integration_test.go:83: SHOW CREATE TABLE materials:
        CREATE TABLE `materials` (
          `id` bigint unsigned NOT NULL AUTO_INCREMENT,
          `class_id` bigint unsigned NOT NULL,
          `uploader_id` bigint unsigned DEFAULT NULL,
          `original_name` varchar(255) COLLATE utf8mb4_0900_as_cs NOT NULL,
          `storage_key` varchar(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
          `size` bigint unsigned NOT NULL,
          `source` enum('seed','upload') COLLATE utf8mb4_0900_as_cs NOT NULL,
          `created_at` datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
          PRIMARY KEY (`id`),
          UNIQUE KEY `uq_materials_id_class` (`id`,`class_id`),
          UNIQUE KEY `uq_materials_storage_key` (`storage_key`),
          KEY `idx_materials_class_id` (`class_id`),
          KEY `idx_materials_uploader_id` (`uploader_id`),
          CONSTRAINT `fk_materials_class` FOREIGN KEY (`class_id`) REFERENCES `classes` (`id`),
          CONSTRAINT `fk_materials_uploader` FOREIGN KEY (`uploader_id`) REFERENCES `users` (`id`),
          CONSTRAINT `ck_materials_source` CHECK ((`source` in (_utf8mb4'seed',_utf8mb4'upload'))),
          CONSTRAINT `ck_materials_upload_actor` CHECK (((`source` = _utf8mb4'seed') or (`uploader_id` is not null)))
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs
    schema_integration_test.go:83: SHOW CREATE TABLE knowledge_entries:
        CREATE TABLE `knowledge_entries` (
          `id` bigint unsigned NOT NULL AUTO_INCREMENT,
          `material_id` bigint unsigned NOT NULL,
          `class_id` bigint unsigned NOT NULL,
          `extracted_text` longtext COLLATE utf8mb4_0900_as_cs NOT NULL,
          PRIMARY KEY (`id`),
          UNIQUE KEY `uq_knowledge_material_id` (`material_id`),
          KEY `idx_knowledge_class_id` (`class_id`),
          KEY `idx_knowledge_material_class` (`material_id`,`class_id`),
          CONSTRAINT `fk_knowledge_class` FOREIGN KEY (`class_id`) REFERENCES `classes` (`id`),
          CONSTRAINT `fk_knowledge_material_class` FOREIGN KEY (`material_id`, `class_id`) REFERENCES `materials` (`id`, `class_id`)
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_as_cs
    schema_integration_test.go:99: all five business tables empty after migration
    schema_integration_test.go:144: PASS scrypt hash stored; plaintext never inserted
    schema_integration_test.go:122: PASS duplicate username: MySQL error 1062
    schema_integration_test.go:122: PASS plaintext password format: MySQL error 3819
    schema_integration_test.go:122: PASS user class foreign key: MySQL error 1452
    schema_integration_test.go:122: PASS role enum: MySQL error 1265
    schema_integration_test.go:154: PASS anonymous session accepts NULL user_id and 32-byte HMAC digest
    schema_integration_test.go:122: PASS duplicate session digest: MySQL error 1062
    schema_integration_test.go:122: PASS session user foreign key: MySQL error 1452
    schema_integration_test.go:122: PASS materials class NOT NULL: MySQL error 1048
    schema_integration_test.go:122: PASS materials class foreign key: MySQL error 1452
    schema_integration_test.go:122: PASS duplicate storage key: MySQL error 1062
    schema_integration_test.go:122: PASS source enum: MySQL error 1265
    schema_integration_test.go:122: PASS upload requires uploader: MySQL error 3819
    schema_integration_test.go:122: PASS duplicate material knowledge link: MySQL error 1062
    schema_integration_test.go:122: PASS knowledge class NOT NULL: MySQL error 1048
    schema_integration_test.go:122: PASS knowledge material foreign key: MySQL error 1452
    schema_integration_test.go:122: PASS knowledge class must match material: MySQL error 1452
    schema_integration_test.go:179: PASS transaction rollback: all five business tables empty
--- PASS: TestMySQL8Schema (0.07s)
PASS
ok  	campusclaw/backend/internal/schema	0.718s
PASS isolated MySQL 8.0 migration and constraint verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

## 回归检查

最终文件下执行 `go test ./... -count=1`、`go build ./...`、`go vet ./...` 均退出码 0。普通测试不带 TEST_MYSQL8 时数据库集成测试会跳过；以上独立脚本运行已显式开启并通过真实 8.0 测试。

```text
?    campusclaw/backend/cmd/migrate       [no test files]
ok   campusclaw/backend/cmd/server        1.029s
ok   campusclaw/backend/internal/config   0.461s
?    campusclaw/backend/internal/db       [no test files]
ok   campusclaw/backend/internal/httpapi  0.722s
ok   campusclaw/backend/internal/schema   1.335s
?    campusclaw/backend/migrations       [no test files]
```

build 与 vet 成功无输出。

## Git 跟踪 verify

在仓库根执行 `git add -- backend/migrations/000001_initial_schema.up.sql backend/migrations/000001_initial_schema.down.sql backend/migrations/migrations.go` 后，`git ls-files -- backend/migrations` 返回：

```text
backend/migrations/000001_initial_schema.down.sql
backend/migrations/000001_initial_schema.up.sql
backend/migrations/migrations.go
```

仅表示已加入 Git 索引，本轮未创建 commit。未将其他既有改动一起暂存。

## 限制

密码列只存哈希；CHECK 只检查编码格式，不能证明任意输入在密码学上有效。测试确实用随机密码和盐计算 scrypt 后存储，未输出哈希或密码。迁移不实现密码业务。SQL down 已提供但本轮未执行；未测试 DDL 故障恢复或 dirty 人工修复，不宣称 MySQL DDL 能整体事务回滚。
