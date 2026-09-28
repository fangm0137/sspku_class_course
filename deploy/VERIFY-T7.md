# 第 7 组真实验收记录

执行日期：2026-09-27 至 2026-09-28。T7.1–T7.4 全部通过指定检查，包括实际容器代理、保留卷重建与备份恢复。下文环境阻塞段保留早期快照，最新进展见末尾。

## Compose 配置（T7.1）

本机原无 Docker CLI/Engine。仅在临时目录取得官方便携 Docker CLI 28.4.0、Compose v2.39.4，用于不连接 Engine 的配置解析。

仓库根目录执行：

```powershell
./deploy/verify-config.ps1 -DockerCommand "$env:TEMP/campusclaw-container-tools/docker/docker.exe" -DockerConfigDirectory "$env:TEMP/campusclaw-container-tools/config"
```

脚本实际执行 `docker compose --env-file <临时空文件> -f compose.yaml config --format json`，使用随机测试值，只在内存解析含凭据的完整配置。退出码 0，输出：

```text
PASS docker compose config: api/mysql/nginx; only 127.0.0.1:8080 published
PASS mysql_data -> /var/lib/mysql; material_data -> /data; Nginx has no data mounts
PASS independent database readiness and HTTP liveness; no build secrets; no root secret in API
```

此项证明配置可解析和服务/卷/凭据边界符合规划，不证明镜像能够构建或容器已运行。

## 存活与数据库就绪（T7.2）

在 backend 目录执行：

```powershell
$env:GOPATH = Join-Path $env:TEMP 'campusclaw-gopath'
$env:GOCACHE = Join-Path $env:TEMP 'campusclaw-gocache'
./scripts/verify-schema.ps1 -Suite health -MySQLBin "$env:TEMP/campusclaw-mysql-8.0.46/mysql-8.0.46-winx64/bin" -GoCommand "$env:TEMP/campusclaw-go1.27.1/go/bin/go.exe"
```

脚本创建独立随机端口、临时数据目录及临时数据库，迁移两次，再实际调用：

```text
go test ./internal/httpapi -run "TestAnonymousHealth|TestMySQL8HealthRecovery" -v -count=1 -timeout 120s
=== RUN   TestAnonymousHealthNeedsNoDependencies
--- PASS: TestAnonymousHealthNeedsNoDependencies (0.00s)
=== RUN   TestMySQL8HealthRecovery
PASS actual MySQL shutdown: health 200, authenticated materials 503
PASS MySQL restart: SELECT 1 ready; same Cookie and persisted materials usable without login
--- PASS: TestMySQL8HealthRecovery (5.45s)
PASS
ok campusclaw/backend/internal/httpapi 6.480s
PASS isolated MySQL 8.0 health verification
Isolated server stopped; temporary data retained for inspection, no existing database was modified.
```

以上保留关键输出；主动关闭数据库时驱动另有连接中断诊断。测试真实关闭并重启 MySQL 8.0.46，验证原 Cookie 与原材料列表，health 不需要数据库或材料目录。早期两次试跑暴露 Windows 关闭时序：连接失败早于文件释放；最终测试等待服务器日志确认完全停止后才重启，最终通过。该检查不是 Docker 卷重建检查。

后续在 backend 执行同一 Go 工具的 `test ./...`、`build ./...`、`vet ./...`，均退出 0。普通 test 不设置 TEST_MYSQL8，会跳过集成测试；真实数据库证据来自上面的 health 专项运行。

## 未执行项与环境阻塞

实际执行便携客户端 `docker version --format '{{.Server.Version}}'`，退出非零，报错包含：

```text
open //./pipe/docker_engine: The system cannot find the file specified.
```

尚未执行镜像构建、Nginx 代理的九路由/5 MiB 上传/Go 停机验收、Compose 完整初始化、保留卷重建或备份恢复。因此 T7.3、T7.4 未勾选，第 8 组未开始。用户选择本机 Docker Desktop，要求安装包放 E 盘新文件夹；安装和引擎就绪后继续上述验收。

本机只读检查：Windows 11 家庭版 64 位、版本 10.0.26200，固件虚拟化和 SLAT 均为 true；`wsl --status` 明确返回“未安装适用于 Linux 的 Windows 子系统”。用户选择仅下载 Docker Desktop 安装包，自行安装；未启用 Windows 组件、未安装 WSL、未重启电脑。

安装包已完整下载至 `E:\DockerDesktopDownload\Docker Desktop Installer.exe`，大小 625204656 字节。2026-09-28 使用 `Get-AuthenticodeSignature` 检查，Status=Valid、Message=Signature verified.，签名者 Docker Inc。下载与签名验证不代表已经安装或启动 Docker Engine。

2026-09-28 用户自行安装后继续检查，Windows 卸载注册项确认 Docker Desktop 位于 `E:\Docker\Docker`，该目录存在程序及 CLI；检查时尚无 Docker Desktop/backend 进程，默认 Engine 管道仍不存在。此前“未安装”的环境快照不再代表当前安装状态，容器运行证据仍待引擎启动后补充。

最终规约与差异检查：

```text
openspec validate add-auth-rbac-class-knowledge --strict
Change 'add-auth-rbac-class-knowledge' is valid
openspec list
add-auth-rbac-class-knowledge  20/25 tasks
git diff --check
# 退出 0；只有既有 LF/CRLF 提示，无空白错误。
```

## Docker 就绪后的真实代理验收（2026-09-28）

已启动用户安装在 E 盘的 Docker Desktop，Linux Engine 返回版本 29.8.0。实际从官方镜像构建 Go API 与 React/Nginx，均成功；前端构建输出 `PASS build contains no server credential names or supplied values`。

仓库根目录执行：

```powershell
$env:DOCKER_HOST='npipe:////./pipe/dockerDesktopLinuxEngine'
./deploy/verify-proxy.ps1 -DockerCommand 'E:\Docker\Docker\resources\bin\docker.exe' -DockerConfigDirectory "$env:TEMP/campusclaw-container-tools/config" -VerifyPersistence
```

脚本创建独立项目 `campusclaw-t7-df21087c`，随机端口、测试密码与会话密钥仅保留进程环境。初始化输出 migrations applied、accounts initialized、material seed initialized。代理验收实际输出：

```text
PASS Nginx nine routes: HTML page, JSON API, login/me/logout, list/detail, exact 5 MiB upload/download hash
PASS Nginx Go-unreachable health: 503 safe error/message JSON
PASS T7.3 actual container proxy verification
```

首次运行在上述接口与停机检查通过后，发现 Compose 不支持 `start --wait`。已将脚本和部署说明中的恢复命令改成 `up -d --wait`，第二次实际恢复成功。未改动业务代码或主规约。

## 持久化与备份恢复（T7.4）

同一次 `-VerifyPersistence` 运行继续输出：

```text
PASS consistent stopped-writer backup: MySQL dump and material archive both nonempty
PASS down/up preserves both volumes: original Cookie, material text and file hash survive
PASS restore to new project: original session, joined material/knowledge text and file SHA-256 match
PASS T7.4 deployment, stop/recreate, backup and restore instructions
Test project stopped; its named volumes retained: campusclaw-t7-df21087c
```

脚本最终退出码 0。先停止 Nginx/API 保持 MySQL 可导出，使用 README 中相同的 mysqldump/tar 命令备份数据库与文件；随后 down/up 保留两个卷，以原 Cookie 验证身份、两表关联正文和原文件 SHA-256。再创建全新 `campusclaw-t7-df21087c-restore` 项目，导入 SQL、恢复文件后才启动 API，使用同一 Cookie 及文件哈希验证成功。

原测试项目和恢复项目都已 down，命名卷保留；备份与边界文件留在系统临时目录的同名子目录，不提交 Git、不打印 SQL 内容。未启动供用户长期使用的默认 campusclaw 项目。第 8 组仍未勾选：本次不把单一教师会话与文件恢复等同于三账号完整矩阵、过期会话或全部 Scenario 映射。

完成后的最终校验：`openspec validate add-auth-rbac-class-knowledge --strict` 返回 `Change 'add-auth-rbac-class-knowledge' is valid`；`openspec list` 返回 `22/25 tasks`。T7.1–T7.4 全部勾选，第 8 组三条任务仍未勾选。
