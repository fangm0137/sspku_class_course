# CampusClaw 迭代1

价值：让班级成员在权限边界内获得可复用的教学资料。
场景：教师登录并上传资料，同班学生登录后读取，其他班级不可读取。
本迭代不做：智能问答、聊天、作业批改、注册找回、校园SSO和生产高可用。

当前阶段：第3课
项目介绍及智能体交接见 [CampusClaw 项目报告](docs/CampusClaw-智能体交接报告.md)。
变更目录：openspec/changes/add-auth-rbac-class-knowledge/
当前已实现 Go API 与 React 班级资料室。开发启动见 [后端说明](backend/README.md) 与 [前端说明](frontend/README.md)；账号及样本初始化见 [验收数据](backend/FIXTURES.md)。

Compose、镜像构建与 Nginx 配置已提供，部署、停止和备份恢复见 [容器部署说明](deploy/README.md)。Compose 配置、镜像构建、代理上传下载、真实 MySQL 停机/恢复、保留卷重建与备份恢复验证均已通过；第 8 组三账号权限、会话重建/过期、初始化幂等和场景覆盖验收也已完成。已执行证据及限制见 [第 7 组验收记录](deploy/VERIFY-T7.md)。

最终验收见 [第 8 组记录](deploy/VERIFY-T8.md) 与 [42 个场景证据](deploy/SCENARIO-EVIDENCE.md)。当前 change 的 25 项任务全部完成；测试容器已停止，实际使用请按部署说明配置并启动。
