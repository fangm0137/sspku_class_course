# 登录接口（switch-auth-to-jwt-token）

`POST /api/login` 已实现。需要先配置环境、迁移数据库、显式执行 cmd/seed 再启动服务，命令见 README。没有默认密码，使用初始化时三个 SEED_*_PASSWORD 环境变量提供的密码。

调用步骤只有一步：直接 POST `application/json` 的 username/password，不需要 Cookie、Origin 或 CSRF 令牌。不要把密码写在 URL、文件或终端历史中。

用户名 1–64 个 Unicode 码点且不能全为空白；密码 1–128 个码点，不裁剪。请求体最多 16 KiB，必须是单一 JSON 对象；重复字段名、缺失/null/空串、类型错误、越界、非法 UTF-8、尾随 JSON 或错误 Content-Type 统一返回 400。仅使用小写 username/password，其余字段被忽略，role/class_id 来自数据库。

| 状态 | JSON 内容 |
| --- | --- |
| 200 | `{"user":{"id":<整数>,"username":"<账号>","role":"teacher或student","class_id":<整数>},"token":"<JWT>","expires_at":"<RFC3339>"}` |
| 400 | `{"error":"invalid_request","message":"请求无效"}` |
| 401 | `{"error":"invalid_credentials","message":"账号或密码错误"}` |
| 503 | `{"error":"service_unavailable","message":"服务暂不可用"}` |

所有响应 application/json、no-store。不存在账号、错误密码、损坏/不支持的哈希返回相同 401。固定 scrypt 参数 N=32768、r=8、p=1，16 字节随机盐、32 字节派生值；占位验证也执行同成本 KDF，不代表系统存在默认密码。

成功后**不再建立服务端会话、不下发 Cookie**，而是签发 HS256 的 JWT：头部固定 `{"alg":"HS256","typ":"JWT"}`，载荷含 `sub`（用户 ID）、`username`、`role`、`classId`、`iat`、`exp`，有效期固定 8 小时、不滑动续期。签名密钥来自 `TOKEN_SECRET`。token 结构、校验顺序与客户端存放要求见 [TOKEN.md](TOKEN.md)。

两次成功登录各自签发独立 token，互相不影响；不存在旧会话失效或并发替换语义。查询账号记录失败返回安全 503，不签发 token。

T2.2 的验证记录（当时为 Cookie 会话方案）见 [VERIFY-T2.2.md](VERIFY-T2.2.md)。token 方案下 GET /api/me 与 POST /api/logout 的调用顺序见 [AUTH.md](AUTH.md)，材料读取见 [MATERIALS.md](MATERIALS.md)。
