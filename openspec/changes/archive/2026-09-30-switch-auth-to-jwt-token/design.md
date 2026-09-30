# Design

## Context

第 3 课的鉴权由三部分组成：`sessions` 表（服务端会话）、`campusclaw_session` HttpOnly Cookie（不透明会话 ID）、`X-CSRF-Token`（绑定会话的写请求保护令牌）。本变更把认证本身替换为无状态 JWT，并移除随 Cookie 一起存在的 CSRF 机制，其余业务规则（班级隔离、上传入库、错误语义、健康检查）保持不变。

约束：Go 1.26 标准库 + 三个既有依赖；`backend/go.sum` 不做依赖新增，因此 JWT 必须手工实现；Docker 构建阶段只编译 `cmd/*`，不编译测试文件。

## Goals / Non-Goals

**Goals**

- 登录签发 JWT，受保护接口只认 `Authorization: Bearer <token>`；DevTools 中受保护请求不再出现身份 Cookie。
- 保留第 3 课已验证的班级隔离强度：角色与班级仍以数据库账号记录为准，客户端参数不能扩大权限。
- 失败语义不退化：无 token / 非法 token 为 401，数据库不可用为 503，跨班与不存在材料同为 404。

**Non-Goals**

- 不做 refresh token、吊销名单、多设备会话、OAuth/SSO、登录限流。
- 不改动材料上传、文件存储、知识库入库与前端页面结构。

## Decisions

### D1. 使用 HS256 无状态 JWT，标准库手写实现

Header 固定为 `{"alg":"HS256","typ":"JWT"}`；Payload 为：

```json
{"sub":1,"username":"teacher_a","role":"teacher","classId":1,"iat":1759190000,"exp":1759218800}
```

签名 = `HMAC-SHA256(base64url(header) + "." + base64url(payload), TOKEN_SECRET)`，编码为 base64url 无填充；校验用 `hmac.Equal` 做恒定时间比较。

不引入 `golang-jwt` 等第三方库：一是避免改动 `go.sum` 与构建网络的额外风险，二是本用例只支持单算法单密钥，手写实现的攻击面更小（显式拒绝 `alg` 不为 `HS256` 的 token，天然免疫 `alg=none` 混淆）。

**校验顺序**：拆分三段 → base64url 解码 header/payload/signature → header.alg 必须为 HS256 → 重新计算签名并恒定时间比较 → `exp` 必须晚于当前时间 → 返回 payload。

**有效期**：8 小时，与第 3 课登录会话时长一致，无滑动续期。

### D2. 身份以数据库账号记录为准，token 只承载账号 ID

`RequireIdentity` 解析出 `sub` 后，用 `SELECT id,username,role,class_id FROM users WHERE id=?` 回查。理由：

- 保持"服务端可信账号记录决定角色与班级"的既有安全属性；token 载荷中的 `role`/`classId` 只作为签发时的记录，不参与鉴权判断。
- 账号被删除后 token 立即失效（401），无需吊销名单。
- 代价是每个受保护请求多一次主键查询，与第 3 课"每个请求 JOIN sessions/users"的成本同数量级，可接受。

### D3. 移除 CSRF 而不是保留

CSRF 的前提是"浏览器会自动附带凭据"（Cookie）。Token 放在 `Authorization` 头里，浏览器不会自动附加，攻击者页面要构造该请求必然触发 CORS 预检，而服务端不返回任何 CORS 许可头，预检失败即请求不发出。因此：

- 删除 `GET /api/csrf`、`X-CSRF-Token` 校验、`sameOrigin` 判定与 `csrf_*` 相关代码路径。
- 登录接口虽然仍接受 JSON，但 `Content-Type: application/json` 同样会触发跨源预检，不构成登录 CSRF 面。

**权衡**：若将来引入 Cookie 承载的凭据或放开 CORS，必须重新评估 CSRF 保护。

### D4. 登出改为客户端丢弃 token

无状态 token 服务端没有可删除的记录。`POST /api/logout` 仍要求 token 有效（无效返回 401），成功后返回 200 与 `{"ok":true}`，前端清空 localStorage 与内存状态。

不做 token 黑名单：本次迭代没有多设备管理与强制下线需求，黑名单会重新引入服务端状态并需要过期清理策略。

### D5. 前端把 token 存在 localStorage

`src/api.ts` 维护单一 token 来源（localStorage + 内存副本），`response()` 在存在 token 时统一附加 `Authorization: Bearer <token>`；401 时清除 token 并让 UI 回到登录页。

第 3 课原本禁止把会话标识存入 Web Storage，本变更显式反转该约定并在此重新论证：JWT 不携带密码，风险集中在 XSS；当前应用对材料文本做转义渲染、不注入 HTML，且已通过浏览器脚本注入用例验证。选择 localStorage 而非纯内存，是为了满足"刷新页面仍处于登录态"的既有交互契约。

**遗留说明**：`Authorization` 头与同源 Cookie 不同，`fetch` 的 `credentials` 语义不再影响认证；前端保留 `cache: 'no-store'`，`credentials` 改为 `omit` 以显式表明不依赖 Cookie。

### D6. 删除 `sessions` 表，用新迁移承担

`000001_initial_schema` 保持原样（历史迁移不可改写），新增 `000002_drop_sessions.up/down.sql`。选择删除而不是留空表：

- 表内只有临时登录态，无业务价值，保留会被误读为"仍在用会话"。
- `knowledge_entries` 等业务表与该表无外键关系，删除无级联影响。

### D7. 密钥项 `SESSION_SECRET` 更名为 `TOKEN_SECRET`

语义已由"会话索引摘要密钥"变为"token 签名密钥"。改名涉及 `config.go`、`compose.yaml`、`.env`、`.env.example` 与文档。保留校验：缺失或空值仍拒绝启动，且不打印密钥内容。

改名会影响本地已有 `.env`，因此实施时同步更新本机 `.env` 并重建 api 容器；重建后原有 token（按旧密钥签发的并不存在）无兼容问题。

## 接口响应约定

| 接口 | 成功 | 说明 |
|---|---|---|
| `POST /api/login` | 200 | `{"user":{"id","username","role","class_id"},"token":"<jwt>","expires_at":"<RFC3339>"}`；请求体为 JSON username/password |
| `GET /api/me` | 200 | `{"id","username","role","class_id"}`，不回显 token |
| `POST /api/logout` | 200 | `{"ok":true}`，无服务端会话副作用 |
| `GET /api/materials` | 200 | `{"items":[{"id","title","class_id","created_at"}]}` |
| `GET /api/materials/{id}` | 200 | 列表字段 + `text` |
| `GET /api/materials/{id}/file` | 200 | 二进制附件 |
| `POST /api/materials` | 201 | `{"material_id":N}` |
| `GET /health` | 200 | `{"status":"ok"}`，不查库不验 token |
| 任意错误 | 4xx/5xx | `{"error":"<code>","message":"<统一安全文案>"}` |

错误码：401 `unauthenticated`（无 / 非法 / 过期 token）、403 `forbidden`（学生调用上传）、400 `invalid_request`、413 `payload_too_large`、404 `not_found`、503 `service_unavailable`。优先级 401 > 403 > 400/413 > 404 > 503。

## Risks / Trade-offs

| 风险 | 说明 | 缓解 |
|---|---|---|
| token 无法主动失效 | 用户改密或被删后旧 token 仍有效至到期 | 每次请求回查账号记录，删除账号即失效；改密吊销留待后续变更 |
| localStorage 受 XSS 影响 | 脚本注入可读取 token | 页面不使用 `dangerouslySetInnerHTML`，材料文本经 React 转义；保留浏览器脚本注入用例 |
| token 体积随载荷增长 | 目前载荷固定 6 个字段 | 载荷字段受控，不放入动态数据 |
| 移除 CSRF 的回归面 | 若日后重新引入 Cookie 凭据 | 在 design 与本变更 Non-goals 中显式记录该前提 |

## Migration Plan

1. 新增 `000002_drop_sessions` 迁移。
2. 改名环境变量并重建 api 镜像；执行 `migrate up` 删除 `sessions` 表。
3. 前端重新构建并发布，浏览器端旧 Cookie 不再被读取（可手动清理）。
4. 回滚：`migrate down 1` 重建 `sessions` 表，代码回到第 3 课提交。数据面无损（业务表未改动）。

## Open Questions

- 是否需要在后续变更中增加 token 黑名单与改密强制下线？（当前 Non-goal）
- 是否需要把有效期改为可配置（`TOKEN_TTL`）？当前固定 8 小时，与第 3 课一致。
