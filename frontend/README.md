# CampusClaw 班级资料室

React 18 + TypeScript + Vite 前端。登录后显示本班材料；教师可以上传 TXT/MD，学生只能阅读及下载。页面不提供班级切换、注册、聊天或作业流程。

## 本地启动

需要 Node.js 20.19+ 或 22.12+（本轮使用 24.21.0）与 npm。先按 [后端账号与样本说明](../backend/FIXTURES.md) 在专用数据库中完成迁移、账号和 B 班种子初始化，再启动 Go。Go 默认地址为 http://127.0.0.1:8080；必须使用 backend/CONFIGURATION.md 中的完整环境配置。前端不保存默认账号密码。

在 frontend 目录执行：

```powershell
npm ci
npm run dev
```

访问命令输出的 localhost 地址。开发服务器仅监听 127.0.0.1，同源转发 /api 和 /health 到 Go，并保留 Host，保持后端的 Origin 校验。Go 地址不同可为该前端开发进程设置非凭据变量 CAMPUSCLAW_API_TARGET；不得在 URL 内加入账号密码。该地址不写入客户端构建。

生产构建：

```powershell
npm run build
```

执行 TypeScript 检查、Vite 构建及服务端凭据标记/已提供环境值扫描，输出 dist。Vite 不加载 .env 文件，不向前端注入服务器环境，不设置 VITE_* 凭据。构建产物及 node_modules 不提交，package-lock.json 应纳入版本管理。生产静态服务与 Nginx/Compose 尚属第 7 组，Vite 开发服务器不作为生产部署方案。

## 页面操作

1. 使用初始化时分配的账号与密码登录。页面从服务端恢复角色及班级，不提供用户自选权限。
2. 教师选择一份 TXT/MD 并上传，最大 5 MiB。成功后自动刷新列表并展示新材料。本班学生点击“刷新列表”即可看到。
3. 点击材料查看正文。Markdown 和 HTML 样式内容均作为原文显示，不执行脚本；“下载原文件”通过受保护接口获取附件。
4. 浏览器刷新会调用 /api/me 恢复登录；“退出登录”成功后清除当前页面的材料及身份。学生界面不显示上传控件，服务端也会拒绝学生直接上传。
5. 服务暂不可用时，保留已知身份并显示错误，可刷新重试；首次加载无法确认会话时显示“重新连接”。只有明确的 401 才清除身份。上传结果不确定时先刷新确认，重复上传会生成新的记录。

Cookie 由浏览器管理且为 HttpOnly，页面不读取 Cookie；CSRF token 只在内存中，每次写操作前更新，登录后再次更新。不使用 localStorage/sessionStorage 保存身份或凭据，不自动重放失败上传。多标签页共享 Cookie，令牌被其他标签轮换时可能提示重新操作。

## 可重复浏览器验收

完成 npm ci 后，在 backend 目录执行：

```powershell
./scripts/verify-schema.ps1 -Suite frontend -MySQLBin '<MySQL 8.0 bin 绝对目录>' -GoCommand '<go.exe 绝对路径>'
```

脚本启动隔离 MySQL，执行迁移，Go 测试初始化随机一次性密码，创建真实 HTTP/TLS API 服务，再运行 tests/browser.mjs。测试创建 localhost Vite 服务，通过 Playwright 控制无头浏览器。Windows 优先使用已安装的 Edge；可用 CAMPUSCLAW_BROWSER 指定已安装 Chromium 可执行文件，否则需要安装 Playwright Chromium。Node 需在 PATH 中。

测试结束关闭浏览器、Vite、Go 测试服务与隔离 MySQL。只保存不含凭据的页面截图到被忽略的 test-results，不保存 trace、登录录像、Cookie 快照或请求日志。503 的页面分支使用浏览器响应注入，HTTPS Cookie 使用真实 Go TLS 测试服务；不将这些测试声明为 Nginx 或 Compose 验收。执行结果见 VERIFY-T6.md。
