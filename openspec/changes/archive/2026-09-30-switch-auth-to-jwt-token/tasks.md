# Tasks

> 本变更处于"先规约后实现"流程：以下任务逐项实现并执行 verify 后才勾选。代码实现完成后同步勾选并归档，把 MODIFIED / REMOVED / ADDED 需求写入 `openspec/specs/class-material-access/spec.md`。
>
> 本机没有安装 Go、也没有 PowerShell + 隔离 MySQL 的验收环境，因此 `go build/vet/test` 与前端构建在容器/本机执行，数据库与浏览器套件按各自说明标注。

## 1. 数据模型与配置

- [x] 1.1 新增迁移 `000002_drop_sessions.up.sql` / `.down.sql`，删除已废弃的 `sessions` 表；`000001` 保持不变。
  verify：本机容器实测 `migrate up` 输出 `migrations applied`，`SHOW TABLES` 为 classes/knowledge_entries/materials/schema_migrations/users（无 `sessions`），`schema_migrations` 为 `2/dirty=0`；down 脚本按 `000001` 原定义恢复该表，未在隔离环境执行。
- [x] 1.2 把配置项 `SESSION_SECRET` 更名为 `TOKEN_SECRET`（`internal/config`、`compose.yaml`、`.env.example`、本机 `.env`、deploy 脚本），保持"缺失或空值拒绝启动、不打印密钥"的既有校验。
  verify：`go test ./internal/config/...` 与 `./cmd/server` 通过；不提供 `TOKEN_SECRET` 时启动失败并只提示键名；api 容器以 `TOKEN_SECRET` 正常启动。

## 2. JWT 签发与校验

- [x] 2.1 新增 `internal/httpapi/token.go`：HS256 签发与校验、`Authorization: Bearer` 解析、恒定时间签名比较、显式拒绝非 HS256 算法与过期 token。
  verify：单元/集成测试覆盖合法 token、篡改载荷、错误密钥、`alg=none`、格式损坏、已过期；本机实测篡改签名、`alg=none`、`Basic` 方案均返回 401 `unauthenticated`。
- [x] 2.2 改造 `internal/httpapi/login.go`：保留既有账号密码校验与请求体信封规则，成功后签发 token 并返回 `{"user","token","expires_at"}`，不再写入 `sessions`、不再设置 Cookie。
  verify：实测响应字段恰为 `expires_at`/`token`/`user`，`Set-Cookie` 数量 0，载荷为 `{"sub","username","role","classId","iat","exp"}` 且 `exp-iat = 28800`；错误密码仍为 401 `invalid_credentials`。
- [x] 2.3 改造 `internal/httpapi/materials.go` 的 `RequireIdentity`：从 Bearer token 取账号 ID 并回查 `users` 取得权威角色与班级；无 token / 非法 token 为 401，数据库不可用为 503，学生上传仍为 403。
  verify：实测无 token 401、带 token 200、学生上传 403；集成测试覆盖数据库不可用 503 与伪造角色参数。
- [x] 2.4 调整 `internal/httpapi/session.go`：`GET /api/me` 改为 token 鉴权；`POST /api/logout` 在 token 有效时返回 `{"ok":true}`，不再删除服务端会话。
  verify：实测 `/api/me` 返回四个字段且不含 token 原文；`/api/logout` 返回 200 `{"ok":true}`、不设置 Cookie；数据库无任何会话表。
- [x] 2.5 移除 CSRF 链路并调整路由：删除 `csrf.go` 与 `GET /api/csrf` 路由，共享辅助函数迁到 `security.go`；`handler.go` 的中间件组合改为纯 token 鉴权；清理业务就绪探测里对 `sessions` 的 JOIN。
  verify：实测 `GET /api/csrf` 返回 404 `not_found`；`go build ./...`、`go vet ./...` 无残留引用；api 启动不再出现 `business recovery failed`。

## 3. 前端

- [x] 3.1 `frontend/src/api.ts`：登录响应保存 token 到 localStorage，所有请求附加 `Authorization: Bearer <token>`，仅在 401 `unauthenticated` 时清除 token；删除 CSRF 令牌获取逻辑，`credentials` 改为 `omit`。
  verify：`tsc --noEmit` 与 `vite build` 通过；构建产物通过 `check-dist.mjs` 的凭据标记扫描。
- [x] 3.2 `frontend/src/App.tsx`：登录保存 token、恢复登录态用 token 调 `/api/me`、登出清空 token 与材料。
  verify：类型检查与构建通过；登出调用 `write('/api/logout')` 后经 `signedOut()` 清理 token 与材料。

## 4. 测试与验证脚本

- [x] 4.1 删除 CSRF 专项测试 `csrf_test.go`、`csrf_integration_test.go`；材料边界、请求体信封等仍然有效的用例保留在对应文件中。
  verify：`go test ./...` 编译通过且无对已删除符号的引用。
- [x] 4.2 改造 `login_test.go`、`session_integration_test.go`、`access_integration_test.go`、`upload_integration_test.go`、`health_recovery_integration_test.go`、`frontend_integration_test.go`、`schema_integration_test.go` 与两个 cmd 种子测试到 token 契约。
  verify：`go vet ./...` 通过；默认环境下 `go test ./...` 全部通过（集成套件按既有约定在缺省 `TEST_MYSQL8` 时 skip）。
- [ ] 4.3 改造 `frontend/tests/browser.mjs`：登录后断言请求携带 `Authorization: Bearer`、localStorage 中保存 token、登出后回到登录页；移除 Cookie 与 CSRF 断言。
  verify：脚本已按 token 契约重写（含伪造 token 401、匿名登出 401、HTTPS token）。**待执行**：`-Suite frontend` 需要 PowerShell 7 与隔离 MySQL 8.0 二进制，本机不具备该环境，尚未实跑；需在有该环境的机器上重跑后才算通过。

## 5. 部署与文档

- [x] 5.1 同步部署配置：`compose.yaml` 传入 `TOKEN_SECRET`，重建 api 镜像并执行 `migrate up`。
  verify：`docker compose config --quiet` 通过；三个容器 healthy；实测端到端矩阵见 6.2。
- [x] 5.2 更新受影响文档：`backend/README.md`、`backend/AUTH.md`、`backend/LOGIN.md`、`backend/CONFIGURATION.md`、`backend/UPLOAD.md`、`backend/MATERIALS.md`、`backend/FIXTURES.md`、`frontend/README.md`、`deploy/README.md`，把 `CSRF.md` 替换为 `TOKEN.md`。
  verify：现行文档中不再把 CSRF 描述为当前机制；第 3 课的历史记录（`VERIFY-*.md`、`SCENARIO-EVIDENCE.md`）不改写，并在 AUTH.md 与 deploy/README.md 中标注其时代。

## 6. 端到端验收

- [x] 6.1 结构校验：`openspec validate switch-auth-to-jwt-token --strict` 通过。
  verify：4/4 产物完整。
- [x] 6.2 真实链路验证：登录取 token → 无 token 请求受保护接口为 401 → 带 token 请求 `/api/me` 与 `/api/materials` 为 200 → 学生 token 上传为 403 → 跨班读取为 404 → 篡改 token 为 401。
  verify：在运行中的 Compose 栈实测六步，状态与响应体全部符合预期（另测 `/api/csrf` 404、`Set-Cookie` 为 0、表清单无 `sessions`）。
- [ ] 6.3 截图证据：浏览器登录后打开 DevTools 的 Network，选中 `/api/materials` 请求，确认 Request Headers 中存在 `Authorization: Bearer eyJ...`，且不存在身份 Cookie。
  verify：**待用户执行**：需要人工在浏览器中操作并截图，作为 0930 课堂任务的提交物。
