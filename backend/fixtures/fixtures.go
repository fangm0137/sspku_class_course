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
