package skill

import (
	"strings"
	"testing"
)

// 「工具」同时接受内置工具与 MCP 引用，两者可混写。
func TestParseAcceptsToolReferences(t *testing.T) {
	source := "名称：数学解题引导\n适用条件：方程\n工具：knowledge_search、formula_hint@campus-tools\n启用：是\n\n先看学生写到哪一步。\n"
	definition, err := Parse(source)
	if err != nil {
		t.Fatalf("合法引用应被接受：%v", err)
	}
	want := []string{KnowledgeSearchTool, "formula_hint@campus-tools"}
	if len(definition.Tools) != len(want) {
		t.Fatalf("tools = %v, want %v", definition.Tools, want)
	}
	for index, tool := range want {
		if definition.Tools[index] != tool {
			t.Fatalf("tools[%d] = %q, want %q", index, definition.Tools[index], tool)
		}
	}
	if got := definition.ToolsJSON(); got != `["knowledge_search","formula_hint@campus-tools"]` {
		t.Fatalf("tools json = %s", got)
	}
}

// 省略「工具」时仍按内置工具处理，保持第 5 课的行为。
func TestParseDefaultsToKnowledgeSearch(t *testing.T) {
	definition, err := Parse("名称：语文解题引导\n适用条件：阅读\n启用：是\n\n引导学生找原文。\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(definition.Tools) != 1 || definition.Tools[0] != KnowledgeSearchTool {
		t.Fatalf("tools = %v, want [%s]", definition.Tools, KnowledgeSearchTool)
	}
}

// 工具个数有上限：声明会占上下文，工具多时挤压资料。
func TestParseRejectsTooManyTools(t *testing.T) {
	tools := make([]string, 0, MaxTools+1)
	for index := 0; index <= MaxTools; index++ {
		tools = append(tools, "tool"+string(rune('a'+index))+"@srv")
	}
	source := "名称：数学\n适用条件：方程\n工具：" + strings.Join(tools, "、") + "\n启用：是\n\n引导规程。\n"
	if _, err := Parse(source); err == nil || !strings.Contains(err.Error(), "最多") {
		t.Fatalf("超过上限应被拒绝，实际错误：%v", err)
	}
}
