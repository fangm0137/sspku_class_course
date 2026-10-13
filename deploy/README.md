# 本地容器部署、停止与恢复

部署文件及流程已在 Docker Desktop Linux Engine 29.8.0 上验证：镜像构建、串行初始化、Nginx 代理、保留卷重建以及恢复到全新项目均通过。真实命令和结果见 VERIFY-T7.md；第 8 组三账号访问矩阵、过期 Cookie 和全部 Scenario 证据也已完成，见 VERIFY-T8.md 和 SCENARIO-EVIDENCE.md。

> **变更提示（switch-auth-to-jwt-token）**：认证已由 Cookie 会话改为无状态 JWT，环境项 `SESSION_SECRET` 更名为 `TOKEN_SECRET`（作 HS256 签名密钥），迁移新增 `000002_drop_sessions`，`sessions` 表随之删除。Compose 与 `verify-config.ps1` / `verify-proxy.ps1` 的配置校验已同步改名。
> `verify-e2e.ps1` 中与登录 Cookie、`X-CSRF-Token`、会话表相关的断言仍描述第 3 课方案，尚未按 token 契约重写；`VERIFY-T7.md`、`VERIFY-T8.md`、`SCENARIO-EVIDENCE.md` 是当时的验收记录，不随本次变更改写。以 token 契约重跑完整部署矩阵属于后续待办。
>
> **变更提示（add-agent-skills-and-streaming-qa，第 5 课）**：迁移新增 `000005`（六张表），镜像新增 `seed-skills` 二进制与 `/swagger/` 静态目录，Compose 初始化顺序多一步 `seed-skills`（必须在 `seed` 之后）。Nginx 新增一条正则 location 承载 SSE。

## 前提与配置

使用 Docker Desktop 的 Linux containers 模式及 Docker Compose v2；Windows 示例使用 PowerShell 7.4+，以保证原生程序重定向二进制备份时不改写字节。所有命令从仓库根目录执行。

四个服务为 mysql（MySQL 8.0）、qdrant（向量库）、api（Go）、nginx（Vite 静态页面及反向代理）。仅 Nginx 绑定 `127.0.0.1:8080`；可用 CAMPUSCLAW_PORT 改端口。MySQL、Qdrant 与 API 都不发布宿主机端口，客户端只能经 Nginx 调用本站接口。mysql_data 持久化 MySQL 全部数据（账号、班级、材料、知识库与切片；迁移 000002 后不再有会话表）；qdrant_data 持久化 `campusclaw_chunks` 集合；material_data 挂载 API 的 /data，含 uploads 和 staging。Nginx 不挂载材料卷。

检索相关环境变量（全部可选，缺失时 api 仍能启动）：`QDRANT_URL`（默认 `http://qdrant:6333`）、`QDRANT_COLLECTION`（默认 `campusclaw_chunks`）、`EMBEDDING_BASE_URL`/`EMBEDDING_API_KEY`/`EMBEDDING_MODEL`/`EMBEDDING_DIM`、`CHAT_BASE_URL`/`CHAT_API_KEY`/`CHAT_MODEL`。密钥只注入 api 容器，不进入前端构建产物。第 3 课入库的历史材料没有切片，需要执行一次显式重建：

```bash
docker compose run --rm --no-deps --entrypoint /app/reindex api
```

回滚口径：本变更对第 3 课能力为叠加。停用检索/问答路由与前端入口即可退回原行为；`knowledge_chunks` 与 `qdrant_data` 可直接丢弃，不影响 `materials`、`knowledge_entries` 与账号。接口、字段与降级约定见 [backend/SEARCH.md](../backend/SEARCH.md)。

Nginx 对 SPA 外壳 `/` 与 `/index.html` 返回 `Cache-Control: no-cache`（必须回源校验），对内容哈希命名的 `/assets/` 返回 `public, max-age=31536000, immutable`。这样重建前端后浏览器不会继续使用旧的 index.html 与旧 JS；若症状是"重建后页面行为像旧版本"，先用无痕窗口或硬刷新（Ctrl+Shift+R）排除浏览器缓存。

必需环境项为 TOKEN_SECRET、DB_PASSWORD、MYSQL_ROOT_PASSWORD；初始化另需三个 SEED_*_PASSWORD。DB_NAME 和 DB_USER 在 Compose 中默认 campusclaw，DB_USER 必须为非 root 独立应用账号。容器固定 DB_HOST=mysql、DB_PORT=3306、HTTP_ADDR=0.0.0.0:8080、UPLOAD_DIR=/data/uploads；不使用本机开发目录。MYSQL_ROOT_PASSWORD 仅传给 MySQL，API 不获得 root 密码。

从环境或凭据管理工具提供真实值。可交互读入，避免明文出现在命令历史：

```powershell
function Set-PrivateEnvironmentValue([string]$VariableName) {
    $value = Read-Host "输入 $VariableName" -AsSecureString
    try {
        [Environment]::SetEnvironmentVariable($VariableName, [Net.NetworkCredential]::new('', $value).Password, 'Process')
    } finally { $value.Dispose() }
}
Set-PrivateEnvironmentValue 'TOKEN_SECRET'
Set-PrivateEnvironmentValue 'DB_PASSWORD'
Set-PrivateEnvironmentValue 'MYSQL_ROOT_PASSWORD'
Set-PrivateEnvironmentValue 'SEED_TEACHER_A_PASSWORD'
Set-PrivateEnvironmentValue 'SEED_STUDENT_A1_PASSWORD'
Set-PrivateEnvironmentValue 'SEED_STUDENT_B1_PASSWORD'
```

TOKEN_SECRET 应由可信随机源产生并安全保管，重建时使用同一值，不每次重新生成。若使用本地 .env，复制根 .env.example 后填写并保留 Git 忽略；Compose 会读取它，原生 Go 不会自动读取。不要输出完整 `docker compose config`、环境、Cookie 或请求头；配置静默验证使用 `docker compose config --quiet`。verify-config.ps1 使用随机测试值在内存解析配置，不打印秘密。

## 首次串行初始化与启动

每一步必须成功后再执行下一步。不要在 API 运行时执行材料初始化。

```powershell
docker compose config --quiet
docker compose build
docker compose up -d --wait mysql qdrant
docker compose run --rm --no-deps --entrypoint /app/migrate api up
docker compose run --rm --no-deps --entrypoint /app/seed -e SEED_TEACHER_A_PASSWORD -e SEED_STUDENT_A1_PASSWORD -e SEED_STUDENT_B1_PASSWORD api
docker compose run --rm --no-deps --entrypoint /app/seed-materials api
docker compose run --rm --no-deps --entrypoint /app/seed-skills api
docker compose up -d --wait api nginx
docker compose ps
```

一次性命令基于 api 服务镜像，不增加常驻服务。首次输出分别为 migrations applied、accounts initialized、material seed initialized、skill seed initialized。`seed-skills` 依赖 `seed` 建好的 A 班，**顺序不能提前**；它写入 A 班两份预置技能（数学、语文各一份），幂等重跑输出 no change，教师在页面上改过的技能不会被种子作业覆盖（不一致时报冲突）。重复初始化要先停止 nginx/api，使用相同卷和数据库；正常串行重跑输出 no change，不重置账号密码。出现冲突应检查而非删卷强行重建。

打开 http://127.0.0.1:8080，使用教师 A、学生 A1、学生 B1 初始化密码登录，页面操作见 ../frontend/README.md。Go 启动恢复执行完成前业务可能 503；/health 的 200 只代表进程存活，`up --wait` 的 health 通过不代表所有业务已就绪。

## 路由与故障验收

完整且不会漂移的接口清单见运行中的 `/openapi.yaml`（页面 `/swagger/` 可对照试调）——
它与 `internal/httpapi` 的路由表由单元测试逐条比对。当前路由分组：

| 分组 | 路由 |
| --- | --- |
| 匿名 | `GET /health`、`POST /api/login`、`GET /openapi.yaml` |
| 身份 | `GET /api/me`、`POST /api/logout` |
| 材料 | `POST /api/materials`（教师）、`GET /api/materials`、`GET /api/materials/{id}`、`GET /api/materials/{id}/file`、`POST /api/materials/{id}/reindex`（教师） |
| 检索 | `GET /api/search`、`POST /api/ask` |
| 智能体 | `POST /api/agents`（教师）、`GET /api/agents`、`GET /api/agents/{id}`、`PUT /api/agents/{id}`（教师）、`DELETE /api/agents/{id}`（教师）、`PUT /api/agents/{id}/skills`（教师）、`PUT /api/agents/{id}/materials`（教师） |
| 技能 | `POST /api/skills`（教师）、`GET /api/skills`、`GET /api/skills/{id}`、`PUT /api/skills/{id}`（教师）、`DELETE /api/skills/{id}`（教师） |
| 会话 | `GET /api/conversations`、`POST /api/conversations`、`GET /api/conversations/{id}`、`POST /api/conversations/{id}/messages`（SSE） |

`GET /api/csrf` 与 `/api` 都是 404（前者随 JWT 改造移除），未知 `/api/` 路径仍返回 JSON 而非 React 页面。

**流式回答需要单独放行**：`/api/conversations/{id}/messages` 走 nginx 里那条正则 location，
关闭缓冲与压缩并把读超时提到 180 秒；放在 `/api/` 前缀下会因为 30 秒读超时把长回答截断，
也会因默认缓冲让片段"攒着一次性出现"。

Nginx 保留 Host 与路径，/api/ 和 /health 不回退成 React HTML；不缓存响应、不重试写请求。请求体流式转发，大小限制由 Go 在授权后执行。API 无法连接或超时时入口返回 JSON 503 error/message，不能返回静态 health 成功。Docker DNS 动态解析避免 API 重建换 IP 后仍访问旧地址。

运行验收清单（T7 已完成教师 5 MiB 上传下载、九路由与 Go 停机/恢复，完整矩阵见 T8 已执行记录）：教师上传恰好 5 MiB TXT 后下载核对字节；A1 可见、B1 不可见；学生直接上传 403、匿名 401；未知 /api/ 路径仍为 JSON。登录后停止 MySQL，health 应 200、材料为 503；启动 MySQL 后原令牌可继续访问。停止 api 后经 Nginx 的 health 应为安全 JSON 503，启动 api 后恢复。不要把 503 当作账号退出或自动重放上传。

```powershell
docker compose stop mysql
# 验证 health 200 / 材料 503
docker compose up -d --wait mysql
# 验证原 Cookie 与材料恢复
docker compose stop api
# 验证经 Nginx /health 为 JSON 503
docker compose up -d --wait api
```

## 停止与保留卷重建

日常停止：`docker compose stop`；保留容器恢复：`docker compose up -d --wait`。删除并重建容器：

```powershell
docker compose down
docker compose up -d --wait
```

不运行 `down -v`、`volume prune` 或删除命名卷。down 默认保留卷；重建需要原数据库密码和相同 TOKEN_SECRET。修改初始化密码环境变量不会改变已有 MySQL/应用账号密码。保留两个卷并不自动保留外部环境配置，必须另行安全保管。完整的未过期 Cookie、过期 Cookie、原文件哈希、两表记录验收已在第 8 组通过。

## 一致备份

选择受保护的仓库外目录，设置 `$backup` 为其绝对路径，确保目录存在。先停止 nginx/api，保持 MySQL 运行，阻止写入、初始化和文件清理；同时备份数据库和文件，不只复制其中之一：

```powershell
docker compose stop nginx api
docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump --user="$MYSQL_USER" --default-character-set=utf8mb4 --single-transaction --no-tablespaces --set-gtid-purged=OFF "$MYSQL_DATABASE"' > "$backup/database.sql"
docker compose run --rm -T --no-deps --entrypoint tar api -C /data -czf - uploads staging > "$backup/materials.tar.gz"
# 两个命令都成功后，分别记录文件大小和 SHA-256，并安全保存配置与原 TOKEN_SECRET。
Get-FileHash "$backup/database.sql", "$backup/materials.tar.gz" -Algorithm SHA256
docker compose up -d --wait api nginx
```

备份包含密码哈希、会话索引、教学内容，应限制访问，不提交到 Git。不要输出 SQL 正文。导出失败不得视为可恢复备份；不要为取得成功而删除现有卷。

## 恢复到全新隔离项目

先提供与备份匹配的 DB_NAME、DB_USER、DB_PASSWORD、MYSQL_ROOT_PASSWORD、TOKEN_SECRET，使用不同入口端口，例如 CAMPUSCLAW_PORT=8081。确认新项目没有需保留的数据，不能向正在使用的旧库覆盖导入。以下新项目名仅是恢复演练示例：

```powershell
docker compose -p campusclaw-restore build
docker compose -p campusclaw-restore up -d --wait mysql
docker compose -p campusclaw-restore cp "$backup/database.sql" mysql:/tmp/campusclaw-restore.sql
docker compose -p campusclaw-restore exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --user="$MYSQL_USER" --default-character-set=utf8mb4 "$MYSQL_DATABASE" < /tmp/campusclaw-restore.sql'
docker compose -p campusclaw-restore run --rm -T --no-deps --volume "${backup}:/restore:ro" --entrypoint tar api -C /data -xzf /restore/materials.tar.gz
docker compose -p campusclaw-restore up -d --wait api nginx
```

数据库导入与文件恢复都成功后才启动 API 的清理逻辑。验证账号、材料、文件哈希、两表关联及班级隔离；未过期会话只有在原 TOKEN_SECRET 和会话表都保留时才可能继续有效，过期不会延长。上述流程已在隔离容器执行：原会话、材料/知识库正文和文件 SHA-256 均匹配；完整三账号矩阵与过期会话已由第 8 组验收通过。

本迭代不做生产高可用、SSO、注册找回、作业流程、并发初始化、持续清理自动重试、分页、配额或上传幂等键。第 5 课新增的会话与消息只支持逐个读取（无分页）；技能工具只允许 `knowledge_search`，不引入通用工具调用。

配置参考：[Compose 启动顺序](https://docs.docker.com/compose/how-tos/startup-order/)、[Nginx 代理指令](https://nginx.org/en/docs/http/ngx_http_proxy_module.html)。

## 最终验收复现

在 PowerShell 7.5+、Docker Linux 引擎已启动的环境，从仓库根执行 ./deploy/verify-e2e.ps1。脚本创建随机隔离项目、临时测试凭据和 localhost 端口，结束停止容器并保留测试卷；不启动长期使用的默认项目。真实命令、结果及限制见 [VERIFY-T8.md](VERIFY-T8.md)。
