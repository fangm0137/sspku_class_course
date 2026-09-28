# T3.3 文档验证记录

日期：2026-09-27。新增 FIXTURES.md，README 增加入口；修正 CONFIGURATION.md 中仍称没有路径环境变量的旧说明。本轮不改 Go 代码、不执行初始化、不启动服务或 Compose。

## 逐项核对

| 本项要求 | 文档内容与依据 |
| --- | --- |
| 初始化命令 | migrate up → seed → seed-materials，逐步检查退出码；核对 backend/cmd 下三个 main.go 入口和实际输出字符串 |
| 环境密码输入 | DB_PASSWORD 和三个 SEED_*_PASSWORD 通过 Read-Host -AsSecureString 输入当前进程环境，无密码字面量；Go 不自动读 .env，初始化不需 SESSION_SECRET |
| 三个身份 | teacher_a/teacher/A、student_a1/student/A、student_b1/student/B，与 internal/seed/seed.go 对照；ID 由数据库分配，无教师 B |
| 样本和班级标记 | 三个文件名、三个 CLASS_* 标记、Markdown 标题与 fixtures 原文件逐项一致；A 样本不自动入库 |
| seed/upload | B 初始化 source=seed、uploader_id=NULL；未来教师上传 source=upload、uploader_id 关联教师；固定种子键不是公开地址 |
| 密码保留与重复执行 | 重跑仍需全部密码但不重置；材料幂等核对文件和两表，不自动修复冲突；与两种初始化实现及先前验证记录一致 |
| 当前与预期区别 | 访问矩阵明确标为后续验收目标，未声称材料读取、上传或班级隔离已实现；独立测试库不是用户运行库 |

## 本轮实际检查

在仓库根以 PowerShell 读取 FIXTURES.md，对必需命令/变量/身份/source 文本逐项检查，确认三个命令入口存在；对照三份实际样本。实际输出：

```text
PASS: initialization commands, environment variables, three identities and source semantics covered
PASS: 3 sample markers match actual files
```

以下为实际执行的示例语法检查，不执行代码块：

```powershell
$doc = Get-Content backend/FIXTURES.md -Raw
$blocks = [regex]::Matches($doc, '(?s)```powershell\r?\n(.*?)```')
foreach ($block in $blocks) {
    $parseTokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseInput(
        $block.Groups[1].Value, [ref]$parseTokens, [ref]$parseErrors)
    if ($parseErrors.Count -gt 0) { throw 'PowerShell example syntax invalid' }
}
```

```text
PASS: 3 PowerShell examples parsed without execution
```

检查指南未包含 `$env:*PASSWORD/*SECRET` 的引号字面量赋值，并检查 .env.example 所有配置赋值仍为空；人工复核只有变量名与交互输入，没有真实密码、Cookie 或密钥。这是针对本次文档的检查，不是全仓库密钥审计。

```text
PASS: no literal credential assignments in guide; environment example values remain empty
```

实际执行 `openspec validate add-auth-rbac-class-knowledge --strict`：

```text
Change 'add-auth-rbac-class-knowledge' is valid
```

数据库功能证据沿用 VERIFY-T3.1.md、VERIFY-T3.2.md 的明确日期和环境；本轮没有重跑数据库验收、Go 测试或构建，避免将文档核对伪称业务功能验证。
