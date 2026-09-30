# Proposal

## Why

第 3 课为实现班级隔离选择了"服务端会话 + 不透明 Cookie + CSRF 令牌"的鉴权方案，并在 Non-goals 中明确排除 JWT（讲义：「不签发JWT，不把会话标识或可信角色存入localStorage」）。0930 课堂任务要求把登录认证整体切换为 Token 方案，验收证据是浏览器 DevTools 中受保护请求携带 `Authorization: Bearer <JWT>`。

讲义同时写明「JWT不是天生不安全……更换方案需要重新论证和更新相应 Scenario」。因此本次以一个独立变更重新论证并替换鉴权方案，而不是在对话里口头改变既定约定。

## What Changes

- 登录改为签发 HS256 签名的 JWT，头部固定 `{"alg":"HS256","typ":"JWT"}`，载荷含 `sub`（用户 ID）、`username`、`role`、`classId`、`iat`、`exp`，有效期 8 小时；token 随登录响应体返回，不再下发会话 Cookie。
- 受保护接口改为只从 `Authorization: Bearer <token>` 解析身份：先验签并校验有效期，再用 `sub` 回查 `users` 表取得权威角色与班级。请求体、查询串中的 `role` / `class_id` 一律忽略。
- 前端登录后把 token 存入 `localStorage`，之后所有请求自动附加 `Authorization` 请求头；刷新页面用该 token 调 `GET /api/me` 恢复登录态。
- 移除 CSRF 令牌流程与 `GET /api/csrf`。Token 通过自定义请求头传递，浏览器不会自动附带，不构成跨站请求伪造面；登录、登出、上传随即不再校验 CSRF。
- 登出改为客户端丢弃 token：`POST /api/logout` 在 token 有效时返回 200，不再删除服务端会话。
- 新增迁移 `000002_drop_sessions` 删除已废弃的 `sessions` 表；环境变量 `SESSION_SECRET` 更名为 `TOKEN_SECRET` 作为 JWT 签名密钥，缺失或空值时仍拒绝启动。
- 同步改写受影响的单元/集成测试与前端浏览器验证脚本，使其验证 token 契约而非 Cookie / CSRF 契约。
- JWT 以标准库 `crypto/hmac` + `crypto/sha256` + `encoding/base64` + `encoding/json` 手写实现，**不新增第三方依赖**。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `class-material-access`：
  - 认证方式由服务端会话 Cookie 改为无状态 JWT（Bearer 请求头 + 数据库回查身份）。
  - 接口契约移除 `GET /api/csrf`，登录响应新增 `token` 与 `expires_at` 字段。
  - 数据模型移除 `sessions` 表。
  - 凭据安全条款由"会话摘要密钥 + Cookie 安全属性"改为"token 签名密钥 + token 存放与不泄露"。
  - 本地部署条款移除"会话跨容器重建保留"，改为"未过期 token 跨容器重建仍可用"。

## Impact

- 后端：`internal/httpapi`（新增 token 校验、改写 login / me / logout / materials / handler）、`internal/config`（密钥项改名）、`migrations`（新增 000002）、受影响测试。
- 前端：`src/api.ts`（token 存放与请求头）、`src/App.tsx`、`tests/browser.mjs`。
- 部署：`compose.yaml`、`.env.example`、本地 `.env` 的密钥项改名。
- 数据：删除 `sessions` 表。该表只保存临时登录态，删除不影响账号、班级、材料与知识库数据；`000001` 迁移保持不变，删除动作由 `000002` 承担。
- 依赖：无新增（标准库实现 HS256）；不引入 LangChain、jwt-go 等框架。

## Non-goals

不做 refresh token、token 主动吊销名单 / 黑名单、多设备会话管理、OAuth / SSO、第三方身份源、注册改密、登录限流与登录审计。不做跨班级或平台超管权限。不改变材料上传、入库、班级隔离与既有错误码语义。不把课表练习需求混入 CampusClaw。
