package fixtures

import "embed"

// Files contains source samples; A samples are never inserted by the seed command.
//
//go:embed class-a-intro.txt class-a-notes.md class-b-seed.txt
var Files embed.FS

// Skills 是第 5 课 A 班预置的两份技能文件（数学与语文各一份）。
// 教师也可以自行提交同格式的文件；预置只是为了让课堂演示开箱可用。
//
//go:embed skills
var Skills embed.FS

// Apps 是内置 AI 应用引用的提示词模板主体（第 6 课）。
// 模板体含 {{app_name}}/{{today}}/{{question}} 三个占位符，由服务端渲染一次。
//
//go:embed apps
var Apps embed.FS
