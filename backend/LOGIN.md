# 登录接口（T2.2）

`POST /api/login` 已实现。需要先配置环境、迁移数据库、显式执行 cmd/seed 再启动服务，命令见 README。没有默认密码，使用初始化时三个 SEED_*_PASSWORD 环境变量提供的密码。历史 T2.2 的测试 fixture 不属于当前运行库。

调用顺序：

1. 同源请求 GET /api/csrf，在内存中保存 csrf_token，并保留响应 Cookie。
2. POST /api/login，发送 application/json 的 username/password 字符串，携带该 Cookie、X-CSRF-Token 和匹配的 Origin；不要把密码写在 URL、文件或终端历史中。
3. 成功后接受新的 Cookie，立即再次 GET /api/csrf 获取新 token。旧 Cookie 与旧 token 已失效，不可重放。

用户名 1–64 个 Unicode 码点且不能全为空白；密码 1–128 个码点，不裁剪。请求体最多 16 KiB，必须是单一 JSON 对象；重复字段名、缺失/null/空串、类型错误、越界、非法 UTF-8、尾随 JSON 或错误 Content-Type 统一返回 400。仅使用小写 username/password，其余字段被忽略，role/class_id 来自数据库。

| 状态 | JSON 内容 |
| --- | --- |
| 200 | `{"user":{"id":<整数>,"username":"<账号>","role":"teacher或student","class_id":<整数>}}` |
| 400 | `{"error":"invalid_request","message":"请求无效"}` |
| 401 | `{"error":"invalid_credentials","message":"账号或密码错误"}` |
| 403 | `{"error":"csrf_invalid","message":"请求校验失败"}` |
| 503 | `{"error":"service_unavailable","message":"服务暂不可用"}` |

所有响应 application/json、no-store；先检查 Origin/CSRF，再检查输入，再验证密码。不存在账号、错误密码、损坏/不支持的哈希返回相同 401。固定 scrypt 参数 N=32768、r=8、p=1，16 字节随机盐、32 字节派生值；占位验证也执行同成本 KDF，不代表系统存在默认密码。当前哈希编码详见 design 的 T2.2 段落。

成功时在事务内删除本次请求旧会话、创建新会话，提交后才设置校园会话 Cookie：32 字节随机 ID 的 Base64URL 编码，HttpOnly、SameSite=Lax、Path=/，有效期固定 8 小时、不自动续期；直接 HTTPS 时 Secure。服务端仅保存 HMAC 摘要，未将密码或角色编码进 Cookie。新会话没有 CSRF token，需重新获取。

事务中锁定并重验旧会话及 token，防止并发复用旧会话；读写失败返回安全 503，确定失败回滚后旧会话保留，不发新 Cookie。若提交结果因连接中断无法确认，不宣称失败已回滚，响应为 503；客户端重新获取 CSRF 后重试登录。

T2.2 的验证通过临时 handler 检查到期门禁，历史命令见 [VERIFY-T2.2.md](VERIFY-T2.2.md)。后续 T2.3 已接入 GET /api/me 与 POST /api/logout，使用顺序见 [AUTH.md](AUTH.md)。材料读取已在 T4 接入，见 [MATERIALS.md](MATERIALS.md)。
