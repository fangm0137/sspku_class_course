# Tasks

当前处于逐项实现阶段，未完成任务保持未勾选。每轮只完成一条 task，实际执行 verify 后才勾选。后端预定目录 backend，前端 frontend；后端测试/构建命令在 backend 执行，前端构建在 frontend 执行。每条实现任务同时交付对应测试与必要接口说明，记录真实命令和输出。

顺序为配置与模型 → 登录与会话 → 种子 → 班级隔离 → 上传 → 前端 → Compose → 集成。正式种子前的认证测试使用独立测试用户与临时数据库 fixture；材料上传实现前使用测试 handler 验证授权中间件，均不依赖未来业务功能。测试 fixture 不形成额外产品功能。其余未定边界在对应 task 确需改变合同前先修订规约。

## 1. 数据模型与配置

- [x] 1.1 建立 Go module、HTTP 服务、仅进程存活的 GET /health 和环境配置入口，声明 MySQL 驱动、SESSION_SECRET 与必需数据库配置；verify：在 backend 执行 `go build ./...` 和配置测试，缺失/空密钥或数据库配置时启动失败，不输出凭据。已执行测试、构建、vet 与真实进程 smoke，证据见 backend/VERIFY-T1.1.md。
- [x] 1.2 使用版本化显式 SQL 与参数化连接建立 MySQL 8.0 的五张业务表，保留 sessions 的匿名/CSRF 字段、materials.source；materials/knowledge_entries 的 class_id 均非空并建索引，知识库 material_id 唯一且复合外键保证班级一致。当前无规约要求的其他占位表，不实现种子或业务逻辑；verify：真实 MySQL 8.0 上两次 up、SHOW TABLES、两表 SHOW CREATE TABLE、重复插入/错误外键/NULL 拒绝、事务回滚、测试 scrypt 哈希存储，确认迁移 SQL 被 git 跟踪并记录命令输出。已在 MySQL 8.0.46 完成全部本项 verify，见 backend/VERIFY-T1.2.md。
- [ ] 1.3 编写配置示例及忽略规则说明；verify：逐项比对必需变量、两个数据卷、会话/CSRF 有效期、三个种子密码，确认示例无真实值，日志与构建上下文不包含凭据；不提前依赖前端构建产物。

## 2. 登录与会话

- [ ] 2.1 实现 GET /api/csrf 与匿名/已登录会话令牌绑定，X-CSRF-Token 和同源检查；verify：使用测试 handler 及独立用户 fixture，测试匿名获取、30 分钟/会话到期、重复获取使旧令牌失效、错误令牌 403、匿名身份访问受保护 handler 为 401、查库错误 503，并核对 no-store。
- [ ] 2.2 实现 POST /api/login、8 小时服务端会话和新旧会话替换；verify：独立 fixture 测试教师/学生登录、username 1–64 与 password 1–128 码点边界、缺失/null/空串/超长/类型错误/非法 JSON 为通用 400，凭据错误为同形 401；验证成功 user 对象、Cookie 不透明及原令牌失效，数据库故障为 503。
- [ ] 2.3 实现 POST /api/logout 与 GET /api/me；verify：测试 me 返回 id/username/role/class_id，登出 200 与 ok=true、删除行并清 Cookie、旧 Cookie 重放 401，登出 CSRF 错误 403、数据库故障 503 不误报退出成功；记录三种认证接口及 CSRF 使用步骤。

## 3. 验收数据

- [ ] 3.1 实现显式串行开发初始化，建立 A/B、teacher_a、student_a1、student_b1；verify：执行初始化及正常重复初始化，核对角色/班级、记录数量与密码哈希不变；缺必需密码在任何种子写入前拒绝。不验证非目标中的并发初始化或种子执行中途失败补偿。
- [ ] 3.2 生成两个 A 班待上传样本和 B 班完整种子原文件/材料/知识库记录；verify：三个标记可区分，B source=seed、uploader_id 可空，正常串行重复初始化不重复材料；不新增教师 B 或学生上传例外。早期隔离测试所需 A 班已入库材料使用独立测试 fixture。
- [ ] 3.3 编写样本及账号说明；verify：说明列出初始化命令、环境密码输入、三个身份、各班预期标记、source 字段与 seed/upload 区别，文档无真实密码。

## 4. 班级隔离

- [ ] 4.1 实现可信角色/班级中间件，忽略客户端角色和 class_id；verify：以测试 handler 和第 3 组 fixture 验证学生伪造 teacher 仍 403，身份 401 优先于授权，数据库无法判定身份时 503，拒绝时无文件/数据库副作用，不依赖实际上传 handler。
- [ ] 4.2 实现 GET /api/materials、GET /api/materials/{id}、GET /api/materials/{id}/file；verify：用 A/B fixture 测试 items 列表和空 items、详情 text、原文件附件，同班 200、跨班/不存在同形 404、匿名 401、查库 503；字段与 design 响应表完全一致。
- [ ] 4.3 增加读取入口授权回归测试和接口说明；verify：覆盖三条真实路径、双向直接 ID、伪造 class_id、静态和知识库路径绕过，无泄露；仅检查本班结果，无分页/total 或存储配额功能。

## 5. 上传与入库

- [ ] 5.1 实现单 txt/md、UTF-8/BOM、5 MiB、空白及多文件校验与安全存储名；verify：使用第 3 组样本和测试边界文件，验证合法输入、大小边界、400/413、路径片段不越界，并记录测试命令。
- [ ] 5.2 实现 POST /api/materials 的 file 字段与同步两表事务，挂接认证/角色/CSRF；verify：教师成功为 201 且仅返回 material_id，source=upload、uploader_id 正确；伪造 B 班仍归 A；学生 403、匿名 401、无效 CSRF 403 均无副作用，原文件与 text 标记正确。
- [ ] 5.3 实现确定失败回滚、一次文件补偿、业务就绪前孤儿清理及提交结果核实；verify：分别注入文件/提取/两表写入/确定提交失败，检查 500/503 与无半成品；模拟数据库提交确认丢失，核实前不删文件；清理持续失败时业务 503、health 可响应，不实现自动重试策略。
- [ ] 5.4 验证提交已成功但响应中断及独立重试，并完成上传接口说明；verify：丢弃成功响应后查询原文件和两条关联记录仍在，再提交得到新 material_id 和独立记录；明确无去重/幂等键，错误为统一 error/message，不泄露内部路径。

## 6. 前端

- [ ] 6.1 建立 React 18 TypeScript Vite，实现 csrf 获取、登录、me 身份恢复及登出；verify：在 frontend 执行 `npm run build`，浏览器验证登录后重新获取 token、旧 token 拒绝、刷新恢复、登出，503 不清空身份，Cookie 属性符合 design，构建产物无服务端凭据。
- [ ] 6.2 实现本班列表、教师上传、详情文本及附件下载；verify：教师上传后教师和 A1 刷新均立即看到，B1 不可见，学生无上传控件；页面使用 items/material_id/text 字段，脚本文本被转义不执行，无分页；更新页面操作说明。

## 7. Compose、健康与持久化

- [ ] 7.1 编写 Go 与 Vite 构建、Nginx/Go/MySQL 8.0 三服务 Compose 及两个命名卷；verify：实现阶段执行 `docker compose config`，检查仅 Nginx 暴露 localhost 端口，mysql_data 覆盖数据库含 sessions，material_data 覆盖上传和临时文件，无内置密钥。
- [ ] 7.2 集成第 1 组的 GET /health 存活检查与独立 MySQL 就绪检查；verify：禁止数据库/存储调用的测试替身下 health 200、status=ok；数据库停止时业务 503，health 仍 200；恢复后业务及原有效会话可用，业务就绪与 health 不混同。
- [ ] 7.3 配置 Nginx 静态页面与九个 API 路由，包括 /api/csrf；verify：路径与状态不被改写为 React HTML，经代理登录/登出/上传 5 MiB/下载可用；Go 不可达时 health 为 503 且 error/message 格式，无材料卷挂载或公开下载路径。
- [ ] 7.4 补充 README 的配置、串行初始化、启动、验收、停止与备份恢复说明；verify：实现阶段按说明完整启动，停止/重建保留两个卷，备份覆盖 MySQL 和文件，明确密钥与会话有效性关系、删除卷后果以及本轮非目标。

## 8. 端到端 verify

- [ ] 8.1 经 Nginx 执行三账号访问矩阵与响应契约验收；verify：A 两账号只见 A 标记、B1 只见 B，匿名/学生上传/跨班/伪造参数均按合同拒绝；九个路由返回字段及 error/message 与 design 一致，保存不含凭据的证据。
- [ ] 8.2 保留两个卷与 SESSION_SECRET 重建全部容器；verify：先用原未过期 Cookie（不重新登录）读取本班材料成功，过期 Cookie 仍 401；再验证重新登录、原文件哈希、两表记录、正常串行初始化幂等和双向隔离。
- [ ] 8.3 汇总每个 Scenario 的证据映射；verify：全部场景有真实测试或运行证据，未执行项明示未执行，不把文档校验当功能通过；核对未加入问答/作业等业务，以及并发初始化、种子中途失败补偿、登录限流、分页、总配额、持续清理失败自动重试等非目标。
