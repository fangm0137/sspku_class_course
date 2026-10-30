// Package prompt 负责提示词模板的存储与渲染。
//
// 渲染刻意做得很窄：只认三个占位符，只做一次字面替换，不递归展开。
// 这样"模板里写了什么"与"发出去是什么"之间的差集只有三个已知占位符，
// 报告里可以逐字对照；同时提问内容里出现的 {{...}} 不会被当成模板语法二次执行。
package prompt

import (
	"strings"
	"time"
)

// 支持的占位符。除此之外一律原样保留，不静默清空。
const (
	PlaceholderAppName  = "{{app_name}}"
	PlaceholderToday    = "{{today}}"
	PlaceholderQuestion = "{{question}}"
)

// DateLayout 是 {{today}} 的格式，固定按 UTC 取，避免同一请求因时区不同而不可复现。
const DateLayout = "2006-01-02"

// Render 用一次遍历完成三个占位符的替换。
//
// strings.Replacer 的实现是单遍扫描：匹配到的替换值不会被重新扫描，
// 因此提问里若含 "{{today}}" 字面量，它会原样留在结果中而不会被再替换成日期——
// 非递归正是这里要的语义，不是巧合。
func Render(body, appName, question string) string {
	return RenderAt(body, appName, question, time.Now().UTC())
}

// RenderAt 与 Render 相同，但由调用方给定时点，便于测试与复现。
func RenderAt(body, appName, question string, now time.Time) string {
	replacer := strings.NewReplacer(
		PlaceholderAppName, appName,
		PlaceholderToday, now.UTC().Format(DateLayout),
		PlaceholderQuestion, question,
	)
	return replacer.Replace(body)
}

// Placeholders 返回支持的占位符清单，供接口把"可用占位符"告知前端。
func Placeholders() []string {
	return []string{PlaceholderAppName, PlaceholderToday, PlaceholderQuestion}
}
