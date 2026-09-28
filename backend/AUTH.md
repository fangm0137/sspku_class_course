# 认证接口与调用顺序（T2.3）

运行前按 README 配置环境、迁移数据库，并显式执行 cmd/seed 创建三个验收账号。没有默认密码，使用初始化时环境提供的密码；B 班材料另由 cmd/seed-materials 初始化，读取说明见 [MATERIALS.md](MATERIALS.md)。集成测试的独立数据库 fixture 不属于当前运行库。

## 完整调用顺序

1. GET /api/csrf：保留返回的匿名 Cookie，csrf_token 只存内存。具体 Origin 规则见 [CSRF.md](CSRF.md)。
2. POST /api/login：application/json 的 username/password，加 Cookie、匹配的 Origin、X-CSRF-Token；200 返回 `{"user":{"id":<整数>,"username":"<账号>","role":"teacher或student","class_id":<整数>}}` 并换发 Cookie。输入边界与错误见 [LOGIN.md](LOGIN.md)。
3. 使用新 Cookie 再次 GET /api/csrf，替换内存中的旧 token。登录前 token 已失效。
4. 页面刷新后使用 Cookie GET /api/me，不需要 CSRF。200 直接返回身份对象，读取服务器账号当前值。401 表示需重新登录；503 表示暂不可用，不能将它当作已退出。
5. POST /api/logout：携带当前 Cookie、匹配的 Origin 和新 X-CSRF-Token，不要求请求正文。只有收到 200 与 `{"ok":true}` 才算退出成功。服务端删除本次会话并通过 Set-Cookie 清除浏览器 Cookie，其他设备/会话不被一并退出。
6. 登出后旧 Cookie 请求 /api/me 或再次登出返回 401，旧 token 也无法继续使用。再次登录从第 1 步开始。

不要打印密码、Cookie、token、完整请求头，或将它们写入文件、命令参数、localStorage。以上为接口调用流程；React 页面操作及后续浏览器验收见 [前端说明](../frontend/README.md) 和 [VERIFY-T6.md](../frontend/VERIFY-T6.md)。

## 本轮新增接口响应

| 接口 | 成功 | 失败 |
| --- | --- | --- |
| GET /api/me | 200：`{"id":<整数>,"username":"<账号>","role":"teacher或student","class_id":<整数>}`，仅四个字段 | 401、503 |
| POST /api/logout | 200：`{"ok":true}`，且清 Cookie | 401、403、503 |

全部返回 application/json 与 Cache-Control: no-store。错误只含 error/message：

| 状态 | error | message |
| --- | --- | --- |
| 401 | unauthenticated | 请先登录 |
| 403 | csrf_invalid | 请求校验失败 |
| 503 | service_unavailable | 服务暂不可用 |

没有 Cookie、非法/不存在/匿名/到期会话为 401；登出先检查身份再检查 CSRF，因此匿名请求即使缺 token 也是 401。有效登录身份的缺失/错误/到期 token 或跨源请求为 403。身份查询不采用请求里的角色、班级，也不返回密码、会话摘要或内部路径。

登出在事务内锁定并重新校验会话和令牌，提交删除后才设置清除 Cookie：同名 campusclaw_session、Path=/、空值、Max-Age=0 和过去的 Expires，保留 HttpOnly、SameSite=Lax，直接 HTTPS 时含 Secure。数据库错误返回 503，不设置清除 Cookie、不返回 ok。确定的删除失败回滚保留原会话；提交确认丢失时可能已删除，不能宣称原会话一定保留，应在数据库恢复后通过 /api/me 确认。

本轮验证包含真实 MySQL 与 HTTP handler、直接 HTTPS 请求属性模拟，不包含浏览器、Nginx、容器或材料接口。实际命令和输出见 [VERIFY-T2.3.md](VERIFY-T2.3.md)。
