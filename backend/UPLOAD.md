# 上传与恢复（T5.1–T5.4）

先按 FIXTURES.md 迁移并串行初始化，再启动 Go；配置 UPLOAD_DIR 为专用绝对目录，其同级 staging 也是应用专用目录。禁止两个 Go 实例共用并清理这些目录；初始化命令在服务停止时执行。React 页面操作见 ../frontend/README.md，Compose 尚待后续任务。

## 接口

`POST /api/materials` 只允许已登录教师。同源请求必须携带会话 Cookie、Origin 和登录后重新获取的 X-CSRF-Token。请求为 multipart/form-data，仅一个名为 file 的文件；请求里的 class_id/role 被忽略，归属和上传者取自服务端账号。

接受 txt/md（不区分扩展名大小写）、UTF-8、可选首个 BOM；单文件最多 5 MiB，包含 multipart 边界和普通字段的请求最多 5 MiB + 64 KiB。不接受空白文本、多文件、错误文件字段、非法编码、损坏的 multipart 或超过 255 码点的安全展示名。存储键由服务端生成；客户端路径仅用于提取安全展示名，不能控制存储位置。

成功为 201 JSON，仅 `{"material_id":<正整数>}`，no-store。同步保存原文件、source=upload 材料及唯一知识库记录；text 去掉首个 BOM，Markdown 保留源码，原文件不变。同班账号立即可通过 MATERIALS.md 的三个读取接口访问；其他班级同形 404。

错误为 `{"error":"<code>","message":"<safe text>"}`，无内部路径、凭据和数据库错误详情：

| 状态 | error / message | 触发 |
| --- | --- | --- |
| 400 | invalid_request / 请求无效 | 文件格式、字段、数量、编码、空白、文件名等校验失败 |
| 401 | unauthenticated / 请先登录 | 缺失或无效登录身份 |
| 403 | forbidden / 无权执行此操作 | 学生上传 |
| 403 | csrf_invalid / 请求校验失败 | 教师缺失/错误 token 或同源检查失败 |
| 413 | payload_too_large / 文件超过大小限制 | 文件或请求大小超限 |
| 500 | internal_error / 服务内部错误 | 确定的存储/提取/数据库完整性失败 |
| 503 | service_unavailable / 服务暂不可用 | 数据库不可用、提交结果未知、业务尚未就绪 |

已就绪时按身份 → 教师角色 → CSRF → 输入校验执行，拒绝不会写文件或材料记录。初始化或一致性恢复未完成时，所有业务入口均返回 503；health 仍只表示进程存活。

## 调用示例

在 AUTH.md 已完成登录、取得 `$session` WebRequestSession 和登录后的 `$token`、设置 `$base` 后，以 PowerShell 7 调用（自动设置 multipart boundary，不手写 Content-Type）：

```powershell
$sample = Get-Item './fixtures/class-a-notes.md'
$uploaded = Invoke-RestMethod -Method Post -Uri "$base/api/materials" `
    -WebSession $session -Headers @{ Origin = $base; 'X-CSRF-Token' = $token } `
    -Form @{ file = $sample; class_id = '999' }
Invoke-RestMethod -Uri "$base/api/materials/$($uploaded.material_id)" -WebSession $session
```

示例 class_id 不改变教师真实班级。不要打印 Cookie、token、完整请求头或环境；示例未在实际网络服务执行，真实 API 验收使用 Go HTTP handler 和独立 MySQL。

## 失败与恢复

确定提交前失败会回滚事务，并对临时/最终文件进行一次补偿删除。补偿失败后停止业务，保留文件供启动清理，输出安全日志。

提交报错保守视为未知，返回 503、保留最终文件并停止业务。恢复依赖后人工重启 Go：启动清理只运行一次，先检查五表结构，再清 staging，对最终文件按存储键执行锁定查询，等待未决事务结束并核对材料 ID。有引用保留，无引用才清理；不可确认则不继续，业务保持 503。没有持续失败自动重试。请勿手工删除未知提交的文件或数据库记录来绕过恢复。

成功提交后仅客户端响应中断不会撤销材料，也不关闭业务。客户端可刷新列表确认；重试会生成新的材料 ID、独立文件和记录，没有幂等键或去重。

普通运行中数据库暂时不可用不改变已就绪门禁；数据库恢复后可用原有效会话重试。启动恢复失败、补偿失败或提交未知需要人工处理并重启。本轮故障注入方式及真实证据见 VERIFY-T5.md。
