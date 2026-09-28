# 材料读取与班级隔离（T4.1–T4.3）

按 FIXTURES.md 初始化，server 使用相同的绝对 UPLOAD_DIR。按 AUTH.md 登录，读取请求携带服务端返回的会话 Cookie，不需要 CSRF token。

| 路由 | 成功状态及内容 |
| --- | --- |
| GET /api/materials | 200 JSON：items 数组；每项仅 id、title、class_id、created_at；空班为 `{"items":[]}` |
| GET /api/materials/{id} | 200 JSON：列表单项四字段加 text 字符串，为提取的源码文本 |
| GET /api/materials/{id}/file | 200 原文件字节，application/octet-stream，Content-Disposition: attachment；附件名去掉路径片段和控制字符 |

所有响应 no-store。ID 为正整数，created_at 为 UTC RFC3339。列表按 ID 升序返回全部本班材料，无分页或 total。无 storage_key、凭据、内部路径和知识库内部 ID。Markdown text 为源码，不是 HTML。

每次请求用会话关联 users 取得当前 role/class_id，客户端字段不能覆盖。详情和下载按 ID 与可信班级联合查询，先授权再打开文件。ID 为无前导零的正十进制 uint64。

错误均为 JSON `{"error":"<code>","message":"<safe text>"}`：

| 情况 | 状态 / error / message |
| --- | --- |
| 缺少、无效、过期或匿名会话 | 401 / unauthenticated / 请先登录 |
| 跨班、不存在、认证后非法 ID | 404 / not_found / 资源不存在 |
| 数据库查询失败，无法判定身份或材料 | 503 / service_unavailable / 服务暂不可用 |
| 本班材料缺知识库、文件缺失/不可读/长度不符/非法存储键 | 500 / internal_error / 服务内部错误 |

未注册的静态和独立知识库路径为安全 JSON 404，不公开 UPLOAD_DIR。POST /api/materials 已在 T5 接入，学生返回 403 forbidden/无权执行此操作；上传合同见 UPLOAD.md。业务初始化/恢复未完成时统一 503，health 不受影响。

只运行产品初始化时，教师 A 与学生 A1 列表为空，学生 B1 可读 B 班种子。T4 使用独立 A 班 fixture，T5 已验证教师调用上传 handler 后立即读取及隔离，见 VERIFY-T4.md 与 VERIFY-T5.md。不会向用户库插入 A 班材料；React 页面已在 T6 接入，见 ../frontend/README.md，Nginx 待后续任务。
