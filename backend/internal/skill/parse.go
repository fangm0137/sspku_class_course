// Package skill 负责技能文件的解析与校验，以及"一次提问选至多一个技能"的词法路由。
// 技能定义是教师写的单个文本文件：空行以上为元数据键行，空行以下为引导规程。
package skill

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/mcp"
)

// 长度上限：与数据库 CHECK 一致，两者任一被突破都算配置错误。
const (
	MaxSourceRunes      = 8000
	MaxNameRunes        = 32
	MaxTriggerRunes     = 120
	MaxDescRunes        = 2000
	MaxSubjectRunes     = 32
	KnowledgeSearchTool = "knowledge_search"
	// MaxTools 限制一个技能能声明的工具数：声明会占上下文，工具多时挤压资料。
	MaxTools = 5
)

// IsBuiltinTool 判断是否为平台内置工具（不需要外部服务器）。
func IsBuiltinTool(name string) bool { return name == KnowledgeSearchTool }

// 元数据键名。教师写的是中文键行，冒号兼容全角与半角。
const (
	keyName    = "名称"
	keyTrigger = "适用条件"
	keySubject = "资料"
	keyTools   = "工具"
	keyEnabled = "启用"
)

// ParseError 是面向教师的可读错误：带上行号与字段名，便于直接改文件。
type ParseError struct {
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("第 %d 行：%s", e.Line, e.Message)
	}
	return e.Message
}

func parseErrorf(line int, format string, args ...any) error {
	return &ParseError{Line: line, Message: fmt.Sprintf(format, args...)}
}

// Definition 是解析结果，字段与 skills 表的列一一对应。
type Definition struct {
	Name        string
	TriggerText string
	SubjectText string
	Tools       []string
	Enabled     bool
	Description string
	Source      string
}

// Parse 解析并校验一份技能文件。任何不合规都返回 *ParseError，
// 调用方据此返回 400 与可读文案，不把内部错误透出。
func Parse(source string) (Definition, error) {
	if utf8.RuneCountInString(source) > MaxSourceRunes {
		return Definition{}, parseErrorf(0, "文件超过 %d 字，请精简后再提交", MaxSourceRunes)
	}
	if strings.ContainsRune(source, '\x00') {
		return Definition{}, parseErrorf(0, "文件包含非法字符")
	}
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")

	var (
		definition Definition
		seen       = map[string]int{}
		bodyStart  = -1
	)
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if bodyStart < 0 {
			// 元数据区：空行结束元数据；# 开头的注释行忽略。
			if line == "" {
				bodyStart = index + 1
				continue
			}
			if strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := splitKeyValue(line)
			if !ok {
				return Definition{}, parseErrorf(index+1, "元数据行缺少冒号，应形如「名称：数学解题引导」")
			}
			if _, duplicated := seen[key]; duplicated {
				return Definition{}, parseErrorf(index+1, "字段「%s」重复出现", key)
			}
			seen[key] = index + 1
			if err := applyField(&definition, key, value, index+1); err != nil {
				return Definition{}, err
			}
			continue
		}
		break
	}
	if bodyStart < 0 {
		return Definition{}, parseErrorf(0, "文件缺少空行：空行以上是元数据，空行以下是引导规程")
	}
	definition.Description = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	definition.Source = source
	if err := validate(&definition, seen); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

// splitKeyValue 只按第一个冒号切分，值里可以再出现冒号。
func splitKeyValue(line string) (string, string, bool) {
	for _, separator := range []string{"：", ":"} {
		if at := strings.Index(line, separator); at > 0 {
			return strings.TrimSpace(line[:at]), strings.TrimSpace(line[at+len(separator):]), true
		}
	}
	return "", "", false
}

func applyField(definition *Definition, key, value string, line int) error {
	switch key {
	case keyName:
		definition.Name = value
	case keyTrigger:
		definition.TriggerText = value
	case keySubject:
		definition.SubjectText = value
	case keyTools:
		tools := SplitList(value)
		if len(tools) == 0 {
			return parseErrorf(line, "工具不能为空，接受 %s 或 工具名@服务器名", KnowledgeSearchTool)
		}
		if len(tools) > MaxTools {
			return parseErrorf(line, "工具最多 %d 个，当前 %d 个", MaxTools, len(tools))
		}
		for _, tool := range tools {
			if IsBuiltinTool(tool) {
				continue
			}
			// 这里只校验写法。服务器是否属于本班、是否已连接、工具是否在清单内，
			// 需要访问数据库，由保存接口在写入前逐条检查（四类条件的后三类）。
			if _, err := mcp.ParseRef(tool); err != nil {
				return parseErrorf(line, "工具「%s」既不是 %s，也不是 工具名@服务器名（只允许一个 @）", tool, KnowledgeSearchTool)
			}
		}
		definition.Tools = tools
	case keyEnabled:
		enabled, ok := parseBool(value)
		if !ok {
			return parseErrorf(line, "启用只接受「是」或「否」")
		}
		definition.Enabled = enabled
	default:
		return parseErrorf(line, "未定义的字段「%s」，可用字段：%s、%s、%s、%s、%s",
			key, keyName, keyTrigger, keySubject, keyTools, keyEnabled)
	}
	return nil
}

// validate 接收指针：省略「工具」时要把默认值写回调用方看到的那个 Definition。
func validate(definition *Definition, seen map[string]int) error {
	if strings.TrimSpace(definition.Name) == "" {
		return parseErrorf(0, "缺少必填字段「%s」", keyName)
	}
	if strings.TrimSpace(definition.TriggerText) == "" {
		return parseErrorf(0, "缺少必填字段「%s」", keyTrigger)
	}
	if _, ok := seen[keyEnabled]; !ok {
		return parseErrorf(0, "缺少必填字段「%s」", keyEnabled)
	}
	if length := utf8.RuneCountInString(definition.Name); length > MaxNameRunes {
		return parseErrorf(seen[keyName], "名称超过 %d 字", MaxNameRunes)
	}
	if length := utf8.RuneCountInString(definition.TriggerText); length > MaxTriggerRunes {
		return parseErrorf(seen[keyTrigger], "适用条件超过 %d 字", MaxTriggerRunes)
	}
	if definition.SubjectText != "" {
		if length := utf8.RuneCountInString(definition.SubjectText); length > MaxSubjectRunes {
			return parseErrorf(seen[keySubject], "资料超过 %d 字", MaxSubjectRunes)
		}
	}
	if definition.Description == "" {
		return parseErrorf(0, "空行以下缺少引导规程")
	}
	if length := utf8.RuneCountInString(definition.Description); length > MaxDescRunes {
		return parseErrorf(0, "引导规程超过 %d 字", MaxDescRunes)
	}
	if len(definition.Tools) == 0 {
		// 工具可省略：省略时按本课唯一的工具处理。
		definition.Tools = []string{KnowledgeSearchTool}
	}
	return nil
}

// SplitList 按中英文逗号、顿号、分号与竖线切分多值字段，空值丢弃。
func SplitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '|', '｜':
			return true
		}
		return false
	})
	items := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func parseBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "是", "true", "1", "yes":
		return true, true
	case "否", "false", "0", "no":
		return false, true
	}
	return false, false
}

// ToolsJSON 把工具列表编码成入库用的 JSON 数组字面量。
func (d Definition) ToolsJSON() string {
	parts := make([]string, 0, len(d.Tools))
	for _, tool := range d.Tools {
		parts = append(parts, strconv.Quote(tool))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
