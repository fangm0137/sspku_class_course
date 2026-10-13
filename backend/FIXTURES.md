# 验收账号与材料样本（T3.3）

账号初始化、B 班材料初始化、认证、材料上传/读取及班级授权中间件已实现。读取矩阵见 [VERIFY-T4.md](VERIFY-T4.md)，上传后的访问矩阵见 [VERIFY-T5.md](VERIFY-T5.md)，React 浏览器验收见 [VERIFY-T6.md](../frontend/VERIFY-T6.md)。Compose/Nginx 尚未完成。

## 三个身份与密码来源

| 身份 | 用户名 | role | 班级 | 首次初始化密码环境变量 |
| --- | --- | --- | --- | --- |
| 教师 A | teacher_a | teacher | A | SEED_TEACHER_A_PASSWORD |
| 学生 A1 | student_a1 | student | A | SEED_STUDENT_A1_PASSWORD |
| 学生 B1 | student_b1 | student | B | SEED_STUDENT_B1_PASSWORD |

没有默认密码，也没有教师 B。班级 ID 由数据库分配，不应假定 A=1、B=2；角色和班级来自服务器账号记录。每个密码必须是有效 UTF-8、1–128 个 Unicode 码点且不全为空白，保留其余首尾空格。数据库仅存带独立随机盐的 scrypt 哈希。

## 准备环境与串行初始化

先准备 Go（模块最低 1.26.0）和已运行的 MySQL 8.0.16+，创建专用数据库和有相应建表/读写权限的账号。安装与连接前提见 [后端说明](README.md)，变量含义见 [配置说明](CONFIGURATION.md)。以下在仓库的 backend 目录、同一 PowerShell 会话执行；若 go 不在 PATH，使用已安装 go.exe 的绝对路径代替命令中的 go。

通过运行环境设置 DB_HOST、DB_PORT、DB_NAME、DB_USER，DB_PORT 通常为 3306。不要在命令中直接写入密码；可交互输入到当前进程环境：

```powershell
function Set-PrivateEnvironmentValue([string]$VariableName) {
    $privateInput = Read-Host "输入 $VariableName" -AsSecureString
    try {
        [Environment]::SetEnvironmentVariable(
            $VariableName,
            [Net.NetworkCredential]::new('', $privateInput).Password,
            'Process'
        )
    } finally {
        $privateInput.Dispose()
    }
}
Set-PrivateEnvironmentValue 'DB_PASSWORD'
Set-PrivateEnvironmentValue 'SEED_TEACHER_A_PASSWORD'
Set-PrivateEnvironmentValue 'SEED_STUDENT_A1_PASSWORD'
Set-PrivateEnvironmentValue 'SEED_STUDENT_B1_PASSWORD'

# 非凭据：在当前 backend 目录下选择本地原文件目录。
$env:UPLOAD_DIR = [IO.Path]::GetFullPath((Join-Path (Get-Location) 'data/uploads'))
```

输入不会显示在终端或作为密码字面量进入命令历史，但子进程环境中仍有密码，勿打印完整环境或保存终端转储。根 .env.example 是空值清单，复制为 .env 不会自动被 Go 加载。

依次执行，任一步失败就停止，不继续下一步：

```powershell
go run ./cmd/migrate up
if ($LASTEXITCODE -ne 0) { throw '迁移失败，停止初始化' }
go run ./cmd/seed
if ($LASTEXITCODE -ne 0) { throw '账号初始化失败，停止初始化' }
go run ./cmd/seed-materials
if ($LASTEXITCODE -ne 0) { throw '材料初始化失败' }
go run ./cmd/seed-skills
if ($LASTEXITCODE -ne 0) { throw '技能初始化失败' }
```

| 命令 | 所需环境 | 首次预期输出 | 正常串行重跑 |
| --- | --- | --- | --- |
| go run ./cmd/migrate up | 五个 DB_* | migrations applied | no change |
| go run ./cmd/seed | DB_*、三个 SEED_*_PASSWORD | accounts initialized | no change |
| go run ./cmd/seed-materials | DB_*、绝对 UPLOAD_DIR | material seed initialized | no change |
| go run ./cmd/seed-skills | 五个 DB_* | skill seed initialized | no change |

以上四条命令都不需要 TOKEN_SECRET，不会启动 HTTP 服务。`seed-skills` 依赖 `seed` 建好的唯一 A 班，**不能提前执行**；它把 `fixtures/skills/` 下的数学、语文两份技能写入 A 班，幂等键是 `seed_key`，所以重跑只核对不改写——教师改过的技能与样板不一致时报冲突，而不是被种子作业覆盖。

首次对空库执行完成后，应有 2 个班级、3 个账号、1 条 B 班材料和其唯一知识库记录、2 条 A 班技能；原文件在 UPLOAD_DIR/seed-class-b-v1.txt。初始化不创建登录会话、不创建 A 班材料，也不创建智能体与会话。既有数据库可能有其他数据，不能把这些数量当作清空数据库的理由。

验证重复执行时保留相同数据库和 UPLOAD_DIR，按相同顺序串行重跑。账号密码必须仍全部提供；即使提供新密码，也不会重置已有密码。班级/角色冲突、B 班不唯一、材料或原文件不一致时报错，不覆盖已有数据。失败或中断后的遗留文件需要人工核实；不提供并发初始化、种子中断补偿或自动修复。请勿通过删库或删文件强行取得成功输出。

初始化后可移除仅初始化需要的密码环境变量：

```powershell
foreach ($variableName in @(
    'SEED_TEACHER_A_PASSWORD',
    'SEED_STUDENT_A1_PASSWORD',
    'SEED_STUDENT_B1_PASSWORD'
)) {
    [Environment]::SetEnvironmentVariable($variableName, $null, 'Process')
}
```

启动服务还需由环境提供 TOKEN_SECRET，再执行 `go run ./cmd/server`；密钥由外部安全生成并保存，不能每次重启随意更换（更换会使全部未过期 token 立即失效）。认证调用顺序见 [AUTH.md](AUTH.md)：login 取 token → 带 `Authorization: Bearer` 请求 me / materials → logout 后由客户端丢弃 token。初始化成功不代表 HTTP 服务正在运行，health 成功也不代表材料业务就绪。

## 三份样本与 seed/upload 区别

| 源文件 | 标记 | 初始化行为 | 材料来源 |
| --- | --- | --- | --- |
| [class-a-intro.txt](fixtures/class-a-intro.txt) | CLASS_A_TXT_ONLY | 仅保留文件，由教师 A 调用上传接口 | 上传后为 upload |
| [class-a-notes.md](fixtures/class-a-notes.md) | # A notes、CLASS_A_MD_ONLY | 仅保留文件，由教师 A 上传；正文保留 Markdown 源码 | 上传后为 upload |
| [class-b-seed.txt](fixtures/class-b-seed.txt) | CLASS_B_SEED_ONLY | cmd/seed-materials 保存原文件并写入材料和知识库 | seed |

`source=seed` 表示显式初始化数据，B 班材料 uploader_id=NULL；`source=upload` 表示教师经上传接口提交，uploader_id 关联教师。seed 不是学生上传例外，不提供公开文件地址。B 班存储键 seed-class-b-v1.txt 是内部文件标识，不等于 API 地址；教师上传使用材料 ID 加随机部分的存储键。

三个文件位于 backend/fixtures，采用 UTF-8/LF，嵌入材料初始化二进制。源文件与 UPLOAD_DIR 中的运行原文件用途不同；不要将运行目录放入公开静态目录。课堂测试使用独立 A 班已入库 fixture 不代表产品初始化自动导入 A 班材料。

## 后续完整闭环的预期访问矩阵

以下矩阵已在 T5 通过真实 MySQL 与上传/读取 handler 验证；浏览器/Nginx 的完整闭环仍待后续任务：

| 身份 | 教师 A 上传两份样本后应看到 | 不应看到 | 上传权限 |
| --- | --- | --- | --- |
| teacher_a | CLASS_A_TXT_ONLY、CLASS_A_MD_ONLY | CLASS_B_SEED_ONLY | 仅本班 |
| student_a1 | CLASS_A_TXT_ONLY、CLASS_A_MD_ONLY | CLASS_B_SEED_ONLY | 拒绝，403 |
| student_b1 | CLASS_B_SEED_ONLY | CLASS_A_TXT_ONLY、CLASS_A_MD_ONLY | 拒绝，403 |
| 未登录 | 无材料数据 | 全部材料 | 拒绝，401 |

其他班级或不存在的材料 ID 同形返回 404，请求内 class_id/role 不能扩大权限。仅运行初始化时只有 B 班材料，两个 A 班样本还没上传，A 班列表应为 items 空数组；双向隔离证据来自独立测试中的 A/B 材料，不以空列表代替验收。

## 已有证据及本项边界

账号与密码幂等的真实数据库证据见 [VERIFY-T3.1.md](VERIFY-T3.1.md)，材料与原文件幂等证据见 [VERIFY-T3.2.md](VERIFY-T3.2.md)。这些测试使用隔离实例，不意味着用户运行库已初始化。

本项仅完成文档核对，记录见 [VERIFY-T3.3.md](VERIFY-T3.3.md)，没有重新初始化数据库、启动 Compose 或验证浏览器完整流程。后续仍不做问答、聊天、作业流程、注册找回、SSO 或生产高可用。
