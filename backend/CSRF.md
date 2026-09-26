# CSRF 接口（T2.1）

当前仅新增 `GET /api/csrf`；写请求校验组件通过测试 handler 验证，尚未注册 login/logout/upload。先配置环境、迁移数据库并启动 Go，按 backend README 操作。没有有效 Cookie 时建立 30 分钟匿名会话；匿名 Cookie 不代表已登录。

成功为 200 application/json，正文仅 `{"csrf_token":"<Base64URL token>"}`。首次返回 `campusclaw_session` Cookie：HttpOnly、SameSite=Lax、Path=/、30 分钟，直接 HTTPS 连接另设 Secure。有效旧会话不换 Cookie、不续期。再次获取会替换该会话旧 token；新 token 最多 30 分钟且不超过会话到期时间，边界时刻即无效。

数据库仅保存随机会话 ID 的 HMAC-SHA256 与 token 的 SHA-256 摘要，两者均基于 Base64URL 解码后的 32 字节。写入事务提交后才返回 token/新 Cookie。所有本接口成功/拒绝/依赖失败响应 no-store，不允许跨源读取。跨源为 403 `{"error":"csrf_invalid","message":"请求校验失败"}`；数据库查询、写入或提交失败为 503 `{"error":"service_unavailable","message":"服务暂不可用"}`，不返回内部错误。

同源比较协议、主机及有效端口，归一化默认端口。GET 可不带 Origin；携带时必须合法且匹配。Sec-Fetch-Site 如果存在只允许 same-origin/none。写请求必须提供单一匹配的 Origin 和 X-CSRF-Token，不接受表单 token、Referer 替代或客户端转发头。通过未来 localhost HTTP Nginx 时必须保留外部 Host；TLS 终止代理信任配置尚未实现。

开发终端获取示例（只存入内存，不显示 token 或 Cookie）：

```powershell
$csrfResponse = Invoke-WebRequest -Uri 'http://127.0.0.1:8080/api/csrf' -SessionVariable campusSession
$csrfToken = ($csrfResponse.Content | ConvertFrom-Json).csrf_token
$csrfResponse.StatusCode
$csrfResponse.Headers['Cache-Control']
```

预期只显示 200 和 no-store。后续写请求复用 campusSession，并携带 `Origin: http://127.0.0.1:8080` 与 `X-CSRF-Token`；此时登录等接口仍未实现。不要打印变量内容、保存会话转储或将其放进命令参数。未来登录替换会话后必须重新获取 token。

本轮测试的受保护 handler 在 CSRF 前检查有效登录身份，匿名/无效/到期会话为 401 `unauthenticated / 请先登录`，合法登录身份的错误 token 为 403；无法查询身份为 503。测试用户由隔离 fixture 插入，密码为随机输入计算的 scrypt 哈希，不是产品种子。独立验证与实际输出见 [VERIFY-T2.1.md](VERIFY-T2.1.md)。
