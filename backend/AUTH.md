# 认证接口与调用顺序（switch-auth-to-jwt-token）

运行前按 README 配置环境、迁移数据库，并显式执行 cmd/seed 创建三个验收账号。没有默认密码，使用初始化时环境提供的密码；B 班材料另由 cmd/seed-materials 初始化，读取说明见 [MATERIALS.md](MATERIALS.md)。集成测试的独立数据库 fixture 不属于当前运行库。

本文件描述 token 方案。第 3 课的 Cookie 会话与 CSRF 流程已整体替换，`GET /api/csrf` 不再存在；token 结构、校验顺序与取消 CSRF 的理由见 [TOKEN.md](TOKEN.md)。

## 完整调用顺序

1. POST /api/login：`application/json` 的 username/password，无需 Cookie 或额外请求头。200 返回 `{"user":{"id":<整数>,"username":"<账号>","role":"teacher或student","class_id":<整数>},"token":"<JWT>","expires_at":"<RFC3339>"}`，**不下发任何 Cookie**。输入边界与错误见 [LOGIN.md](LOGIN.md)。
2. 客户端把 token 存入 `localStorage`（键名 `campusclaw_token`），之后每个受保护请求都带 `Authorization: Bearer <token>`。
3. 页面刷新后用该 token GET /api/me。200 直接返回身份对象，读取服务器账号当前值。401 表示 token 缺失、无效或已过期，需重新登录；503 表示暂不可用，不能当作已退出。
4. POST /api/logout：携带同样的 `Authorization` 头，不要求请求正文。200 与 `{"ok":true}` 表示服务端已确认 token 有效。**Bearer token 是无状态的**，服务端没有可删除的记录，真正的登出是客户端丢弃 localStorage 中的副本；因此同一个 token 在到期前仍可用。其他设备/浏览器上的 token 不受影响。
5. 再次登录会签发一个全新的 token，两个 token 互不影响（没有"换发"或"旧会话失效"的语义）。

不要打印密码、token、完整请求头，也不要将它们写入文件或命令参数。以上为接口调用流程；React 页面操作及后续浏览器验收见 [前端说明](../frontend/README.md) 和 [VERIFY-T6.md](../frontend/VERIFY-T6.md)。

## 接口响应

| 接口 | 成功 | 失败 |
| --- | --- | --- |
| POST /api/login | 200：`{"user":{...},"token":"<JWT>","expires_at":"<RFC3339>"}` | 400、401、503 |
| GET /api/me | 200：`{"id":<整数>,"username":"<账号>","role":"teacher或student","class_id":<整数>}`，仅四个字段 | 401、503 |
| POST /api/logout | 200：`{"ok":true}` | 401、503 |

全部返回 application/json 与 Cache-Control: no-store。错误只含 error/message：

| 状态 | error | message |
| --- | --- | --- |
| 400 | invalid_request | 请求无效 |
| 401 | invalid_credentials | 账号或密码错误 |
| 401 | unauthenticated | 请先登录 |
| 403 | forbidden | 无权执行此操作 |
| 503 | service_unavailable | 服务暂不可用 |

缺失 `Authorization` 头、非 `Bearer` 方案、签名错误、算法头不符、结构损坏或已过期的 token 一律 401；账号记录查询所需数据库不可用为 503，而不是 401。`GET /api/csrf` 已移除，返回 404。身份查询不采用请求里的角色、班级，也不返回密码或 token 原文。

登出只校验 token 有效后返回 200，不写数据库、不设置 Cookie，因此不存在"删除失败回滚"或"提交结果不确定"的分支；`/api/me` 在数据库恢复后无需重新登录即可继续使用未过期的 token。

本轮验证包含真实 MySQL 与 HTTP handler、直接 HTTPS 请求。实际命令见 [VERIFY-T2.3.md](VERIFY-T2.3.md)（该文件记录的是第 3 课 Cookie 方案的当时结果，token 方案的实测记录见本变更的 tasks 与 deploy 目录）。
