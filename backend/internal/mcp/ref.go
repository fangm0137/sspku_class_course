package mcp

import (
	"strings"
	"unicode/utf8"
)

// RefSeparator 分隔工具名与服务器名。只允许出现一次，避免"工具@服务器@后缀"这类歧义写法。
const RefSeparator = '@'

// Ref 是一条「工具名@服务器名」引用。技能用这种写法声明自己要用的外部工具。
type Ref struct {
	Tool   string
	Server string
}

// String 还原成对外的写法，用于错误信息与审计。
func (r Ref) String() string { return r.Tool + string(RefSeparator) + r.Server }

// ParseRef 解析一条引用。格式错误统一归为 RefError 的「格式」类。
func ParseRef(raw string) (Ref, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Ref{}, &RefError{Ref: raw, Category: CategoryFormat, Detail: "工具引用不能为空"}
	}
	if strings.Count(trimmed, string(RefSeparator)) != 1 {
		return Ref{}, &RefError{Ref: trimmed, Category: CategoryFormat, Detail: "格式应为 工具名@服务器名，且只允许一个 @"}
	}
	parts := strings.SplitN(trimmed, string(RefSeparator), 2)
	tool, server := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if tool == "" || server == "" {
		return Ref{}, &RefError{Ref: trimmed, Category: CategoryFormat, Detail: "工具名与服务器名都不能为空"}
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return Ref{}, &RefError{Ref: trimmed, Category: CategoryFormat, Detail: "引用中不能含空白字符"}
	}
	if utf8.RuneCountInString(tool) > 64 || utf8.RuneCountInString(server) > 64 {
		return Ref{}, &RefError{Ref: trimmed, Category: CategoryFormat, Detail: "工具名与服务器名都不超过 64 字"}
	}
	return Ref{Tool: tool, Server: server}, nil
}

// 配置期校验的四类条件。运行期只判最后一类的"当前是否可用"。
const (
	CategoryFormat     = "格式"
	CategoryServer     = "服务器"
	CategoryConnection = "连接"
	CategoryCatalog    = "清单"
)

// RefError 指出"哪一条引用、哪一类条件失败"，返回给教师直接可改。
type RefError struct {
	Ref      string
	Category string
	Detail   string
}

func (e *RefError) Error() string {
	return "工具引用「" + e.Ref + "」未通过" + e.Category + "校验：" + e.Detail
}
