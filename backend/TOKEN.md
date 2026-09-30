# Token 认证（switch-auth-to-jwt-token）

登录签发 HS256 签名的 JWT，受保护接口只从 `Authorization: Bearer <token>` 取身份。本文件取代此前的 `CSRF.md`：`GET /api/csrf` 与 `X-CSRF-Token` 已随 Cookie 会话一起移除。完整调用顺序见 [AUTH.md](AUTH.md)。

## Token 结构

| 部分 | 内容 |
| --- | --- |
| Header | 固定 `{"alg":"HS256","typ":"JWT"}`，只接受这一个算法头 |
| Payload | `{"sub":<用户ID>,"username":"<账号>","role":"teacher或student","classId":<班级ID>,"iat":<签发秒>,"exp":<到期秒>}` |
| Signature | `HMAC-SHA256(base64url(header) + "." + base64url(payload), TOKEN_SECRET)`，base64url 无填充 |

有效期固定 8 小时，不滑动续期，无 TTL 环境开关。签名密钥来自环境变量 `TOKEN_SECRET`，缺失或空白时进程拒绝启动；更换密钥会使全部未过期 token 立即失效。

## 校验顺序

1. 拆分三段，任一段缺失或 base64url 解码失败即 401。
2. Header 必须逐字节等于 `{"alg":"HS256","typ":"JWT"}`；`alg=none` 与其它算法在验签前就被拒绝。
3. 用 `hmac.Equal` 做**恒定时间**比较重算签名，签名长度必须为 32 字节。
4. `exp` 必须晚于当前时间（秒级，边界时刻即失效）。
5. 用 `sub` 回查 `users` 表取得权威角色与班级；账号已删除即 401。

Payload 里的 `role` / `classId` 只记录签发时的状态，**不参与鉴权判断**。请求体或查询串中的 `role`、`class_id` 一律忽略。第 5 步决定：token 有效但数据库不可用时返回 503，而不是 401。

## 为什么不再需要 CSRF

CSRF 的前提是浏览器会自动附带凭据（Cookie）。Token 放在自定义请求头里，浏览器不会自动附加；跨源页面要构造该请求必然触发 CORS 预检，而服务端不返回任何 CORS 许可头，预检失败即请求不发出。因此：

- 登录、登出、上传都不再校验 CSRF，也没有 `csrf_invalid` 错误码。
- 登录体仍是 `application/json`，同样会触发跨源预检，不构成登录 CSRF 面。

**前提**：不能把凭据改回 Cookie，也不能放开 CORS；否则必须重新评估 CSRF 保护。

## 登出语义

Bearer token 是无状态的，服务端没有可删除的记录。`POST /api/logout` 只做两件事：校验 token 有效，然后返回 `{"ok":true}`。真正的登出是客户端丢弃 localStorage 中的副本。因此：

- 登出后同一个 token 在到期前**仍然可用**（这一点与第 3 课的旧 Cookie 会话不同）。
- 没有吊销名单、多设备会话管理或强制下线；账号被删除或改密之外的失效方式不在本迭代范围。

## 存储与日志

前端把 token 存在 `localStorage` 的 `campusclaw_token` 键下，页面刷新后据此调用 `/api/me` 恢复登录态。服务端不设置任何身份 Cookie。

服务端 MUST NOT 记录 token 原文、密码或签名密钥。数据库在迁移 `000002_drop_sessions` 后不再有 `sessions` 表，也不存在可用于重放的会话记录。

## 开发终端示例

登录并把 token 只存进内存变量（不显示 token 与密码）：

```powershell
$body = @{ username = 'teacher_a'; password = $env:SEED_TEACHER_A_PASSWORD } | ConvertTo-Json
$login = Invoke-RestMethod -Uri 'http://127.0.0.1:8080/api/login' -Method Post -ContentType 'application/json' -Body $body
$token = $login.token
$login.user.role
```

携带 token 读取本班材料：

```powershell
Invoke-RestMethod -Uri 'http://127.0.0.1:8080/api/materials' -Headers @{ Authorization = "Bearer $token" }
```

不带 `Authorization` 头请求同一地址应为 401。不要把 token、密码或完整请求头写入文件、命令参数或终端历史。

## 失败响应

全部返回 application/json 与 `Cache-Control: no-store`，错误体只含 `error` / `message`：

| 状态 | error | message | 触发 |
| --- | --- | --- | --- |
| 401 | unauthenticated | 请先登录 | 缺失、格式错误、签名错误、算法不符、已过期或账号已删除 |
| 403 | forbidden | 无权执行此操作 | 学生调用上传接口 |
| 400 | invalid_request | 请求无效 | 登录请求体不合法 |
| 401 | invalid_credentials | 账号或密码错误 | 账号不存在或密码错误 |
| 404 | not_found | 资源不存在 | 跨班或不存在材料；`GET /api/csrf` 等已移除路径 |
| 503 | service_unavailable | 服务暂不可用 | 数据库不可用 |

`GET /api/csrf` 已删除，按未知路径返回 404。
