# T8.1–T8.3 最终验收记录

日期：2026-09-28。用户授权完成最后一组。使用 Windows PowerShell 7.6.5、Docker Desktop Linux Engine 29.8.0；产品为 Go/MySQL 8.0/React/Nginx，未修改业务行为或主规约。测试脚本要求 PowerShell 7.5+，用于保留 JSON 时间字符串。

## 实际命令与输出

在仓库根目录执行：

```powershell
$env:DOCKER_HOST='npipe:////./pipe/dockerDesktopLinuxEngine'
./deploy/verify-e2e.ps1 -DockerCommand 'E:\Docker\Docker\resources\bin\docker.exe' -DockerConfigDirectory "$env:TEMP/campusclaw-container-tools/config"
```

成功运行项目为 `campusclaw-t8-4b2f7cd9`。脚本随机生成三个种子密码、数据库密码和会话密钥，只保留在进程环境/测试容器运行配置，输出不含凭据；使用临时空 env 文件防止加载用户 .env。仅 Nginx 发布随机 localhost 端口。构建、初始化和所有 HTTP 请求均实际执行。

关键输出摘录，最终退出码 0：

```text
migrations applied
accounts initialized
material seed initialized
PASS three-account matrix: exact own-class IDs/text/file hashes, forged filters ignored, both-way direct IDs 404
PASS T8.1 nine-route fields/status/error messages, anonymous/forged/student/CSRF rejection without side effects and logout replay
PASS three-account matrix: exact own-class IDs/text/file hashes, forged filters ignored, both-way direct IDs 404
PASS T8.2 original three Cookies before re-login; expired Cookie remains 401; both volumes, rows and hashes unchanged
no change
no change
no change
PASS three-account matrix: exact own-class IDs/text/file hashes, forged filters ignored, both-way direct IDs 404
PASS T8.2 serial migrate/seed rerun: no change, passwords/IDs/text/files preserved, fresh login and both-way isolation
PASS credential check: password hashes only, supplied secrets absent from service logs
PASS all T8.1/T8.2 container checks
Test containers stopped; volumes retained: campusclaw-t8-4b2f7cd9
```

前两次试跑未通过，原因均在验收脚本：PowerShell 将 JSON RFC3339 自动转成日期对象，以及单元素数组自动展开。分别修正为 `ConvertFrom-Json -DateKind String` 和显式数组包装后完整重跑成功；没有修改产品接口以适配测试。失败试跑也停止了隔离容器，保留测试卷。

## T8.1：经 Nginx 的访问矩阵

教师上传真实 `class-a-intro.txt` 和 `class-a-notes.md`，请求同时在查询串和表单指定 B 班、伪造 role，实际仍归 A。B 班保留完整 `class-b-seed.txt`，不是空班级验证。

| 请求者 | 本班读取 | 指定另一班列表参数 | 已知跨班详情/文件 | 直接上传 |
| --- | --- | --- | --- | --- |
| teacher_a | 两份 A 材料，全文及文件 SHA-256 正确 | 仍只返回 A | B ID 与不存在 ID 均相同 404 | 201；伪造 B 仍归 A |
| student_a1 | 两份 A 材料，全文及文件 SHA-256 正确 | 仍只返回 A | B ID 与不存在 ID 均相同 404 | 合法 CSRF、伪造 teacher 仍 403 |
| student_b1 | 仅 B 种子，全文及文件 SHA-256 正确 | 仍只返回 B | 两个 A ID 与不存在 ID 均相同 404 | 合法 CSRF、伪造 teacher 仍 403 |
| 无 Cookie/伪造 Cookie | 三种读取入口 401 | 无材料泄露 | 401 优先 | 匿名上传 401 |

严格比对九个路由的成功字段：health.status、csrf_token、login.user、me 四字段、material_id、items（含空数组）、详情 text、logout.ok；下载为二进制附件。检查 401/403/404 的 error/message 两字段及固定中文消息，无内部字段。其他输入/故障分支沿用 T2/T4/T5/T7 已执行证据，未声称本次逐个重新注入全部 400/413/500/503。

拒绝上传前后比较 classes/users/materials/knowledge_entries 的快照、密码哈希的二次摘要以及 uploads/staging 中全部文件 SHA-256，确认无材料/知识库/文件副作用。登录/CSRF 合法会话变化不被错误地当成材料副作用。登出后原 Cookie 重放读取返回 401。

## T8.2：重建、过期与幂等

额外登录一条学生会话，在独立测试库中用 PREPARE/EXECUTE 参数化匹配其 HMAC 摘要，只调整该行创建/到期时间并清 CSRF，确认恰好影响一行。保持产品八小时期限不变，不改变系统时钟，不等待八小时；该 Cookie 重建前后都返回 401。

实际执行 `down` 和 `up -d --wait`，不使用删除卷选项，保留相同 SESSION_SECRET。**先用原三个未过期 Cookie** 执行完整读取矩阵，无重新登录；逐项比较账号、材料及知识库记录快照、正文与原文件哈希，完全一致。

随后停止 Nginx/API，串行重复 migrate/seed/seed-materials，三者均输出 no change。为教师输入新的测试种子密码，既有密码哈希仍不变，原密码仍能登录。最后三个账号全部重新登录，再验证完整双向隔离、文本及下载哈希。日志只在内存扫描，不打印原日志；扫描提供的密码、密钥和当前会话值未发现泄露，数据库密码列均为 scrypt 格式。

## T8.3：场景覆盖与范围

[SCENARIO-EVIDENCE.md](SCENARIO-EVIDENCE.md) 对应当前 delta 全部 42 个 Scenario，逐项给出真实 spec 行号、测试源文件与已有运行记录。已检查标题集合完整、无重复、相关源文件存在；历史注入测试和实际网络/浏览器运行的边界分别说明。未把普通单元测试跳过的 MySQL 项当成本轮数据库证据。

本轮在 backend 实际执行 `go test ./... -count=1`：cmd/seed、cmd/seed-materials、cmd/server、internal/config、internal/httpapi、internal/schema、internal/seed 均 ok，退出 0；普通运行跳过受环境保护的 MySQL 集成项。本轮真实数据库证据来自上述容器脚本，历史故障注入见对应记录。

范围核对：handler 注册九个产品方法/路由及安全 404 fallback；显式迁移创建五张业务表，另由迁移工具管理 schema_migrations。未新增非目标业务、生产高可用、并发初始化、种子失败补偿、登录限流、分页、配额、持续清理重试或上传幂等键。

所有测试容器已停止，命名卷保留；未启动长期使用的默认 campusclaw 项目。未自动提交 Git、同步主规约或归档 change。

## 最终状态

```text
openspec validate add-auth-rbac-class-knowledge --strict
Change 'add-auth-rbac-class-knowledge' is valid
openspec list
add-auth-rbac-class-knowledge  ✓ Complete
openspec instructions apply --change add-auth-rbac-class-knowledge --json
complete: 25, remaining: 0, state: all_done
```

场景标题集合及所有证据链接检查：PASS 42/42。任务复核：Completed=25、Pending=0。`git diff --check` 退出 0，仅有仓库 LF/CRLF 提示。
