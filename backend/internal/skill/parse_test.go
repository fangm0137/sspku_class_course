package skill

import (
	"errors"
	"strings"
	"testing"
)

// 一份合规的技能文件：空行以上是元数据，空行以下是引导规程。
const validSource = `名称：数学解题引导
适用条件：方程,去括号,分数
资料：数学
工具：knowledge_search
启用：是

学生正在做数学作业。先看懂他卡在哪一步，只指出下一步该检查什么。
`

func TestParseReadsEveryColumn(t *testing.T) {
	definition, err := Parse(validSource)
	if err != nil {
		t.Fatalf("parse valid source: %v", err)
	}
	if definition.Name != "数学解题引导" {
		t.Fatalf("name = %q", definition.Name)
	}
	if definition.TriggerText != "方程,去括号,分数" {
		t.Fatalf("trigger = %q", definition.TriggerText)
	}
	if definition.SubjectText != "数学" {
		t.Fatalf("subject = %q", definition.SubjectText)
	}
	if !definition.Enabled {
		t.Fatal("enabled should be true")
	}
	if len(definition.Tools) != 1 || definition.Tools[0] != KnowledgeSearchTool {
		t.Fatalf("tools = %v", definition.Tools)
	}
	if definition.ToolsJSON() != `["knowledge_search"]` {
		t.Fatalf("tools json = %s", definition.ToolsJSON())
	}
	if !strings.Contains(definition.Description, "先看懂他卡在哪一步") {
		t.Fatalf("description = %q", definition.Description)
	}
	// 原文必须原样保留：接口的读写都以它为准，教师能改回同一份文件。
	if definition.Source != validSource {
		t.Fatal("source text must be preserved verbatim")
	}
}

// 「工具」是可选字段：省略时按本课唯一的工具补齐，而不是留下空列表。
// 这里专门验证默认值能写回调用方看到的那个 Definition（早前按值传递会把它丢掉）。
func TestParseDefaultsToolWhenOmitted(t *testing.T) {
	source := "名称：语文解题引导\n适用条件：默写\n启用：是\n\n只提示该往前看哪一句。\n"
	definition, err := Parse(source)
	if err != nil {
		t.Fatalf("parse without tools: %v", err)
	}
	if definition.ToolsJSON() != `["knowledge_search"]` {
		t.Fatalf("default tools json = %s", definition.ToolsJSON())
	}
}

// 技能停用时仍应解析成功：停用是数据状态，不是格式错误。
func TestParseDisabledSkill(t *testing.T) {
	source := "名称：暂时停用的技能\n适用条件：函数\n启用：否\n\n先看学生写到哪一步。\n"
	definition, err := Parse(source)
	if err != nil {
		t.Fatalf("parse disabled skill: %v", err)
	}
	if definition.Enabled {
		t.Fatal("enabled should be false")
	}
}

func TestParseRejectsBadSources(t *testing.T) {
	cases := []struct {
		name   string
		source string
		expect string
	}{
		{"缺少名称", "适用条件：方程\n启用：是\n\n引导规程。\n", "名称"},
		{"缺少适用条件", "名称：数学\n启用：是\n\n引导规程。\n", "适用条件"},
		{"缺少启用", "名称：数学\n适用条件：方程\n\n引导规程。\n", "启用"},
		{"没有空行", "名称：数学\n适用条件：方程\n启用：是\n", "空行"},
		{"空行后没有正文", "名称：数学\n适用条件：方程\n启用：是\n\n\n", "引导规程"},
		{"字段重复", "名称：数学\n名称：语文\n适用条件：方程\n启用：是\n\n引导规程。\n", "重复"},
		{"字段未定义", "名称：数学\n适用条件：方程\n启用：是\n难度：高\n\n引导规程。\n", "未定义的字段"},
		{"元数据行没有冒号", "名称：数学\n适用条件方程\n启用：是\n\n引导规程。\n", "缺少冒号"},
		{"工具不被支持", "名称：数学\n适用条件：方程\n工具：web_search\n启用：是\n\n引导规程。\n", "不被支持"},
		{"名称超长", "名称：" + strings.Repeat("长", 33) + "\n适用条件：方程\n启用：是\n\n引导规程。\n", "名称超过"},
		{"启用值非法", "名称：数学\n适用条件：方程\n启用：也许\n\n引导规程。\n", "只接受"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Parse(testCase.source)
			if err == nil {
				t.Fatal("expected a parse error")
			}
			var parseError *ParseError
			if !errors.As(err, &parseError) {
				t.Fatalf("error type = %T, want *ParseError", err)
			}
			if !strings.Contains(err.Error(), testCase.expect) {
				t.Fatalf("error %q does not mention %q", err.Error(), testCase.expect)
			}
		})
	}
}

// 全角与半角冒号都要认，教师从不同地方复制文本都可能遇到。
func TestParseAcceptsBothColonStyles(t *testing.T) {
	source := "名称:数学解题引导\n适用条件:方程\n启用:是\n\n引导规程。\n"
	if _, err := Parse(source); err != nil {
		t.Fatalf("halfwidth colons should be accepted: %v", err)
	}
}

// CRLF 与 LF 必须等价：Windows 上编辑过的文件不能因此报错。
func TestParseAcceptsCRLF(t *testing.T) {
	source := strings.ReplaceAll(validSource, "\n", "\r\n")
	if _, err := Parse(source); err != nil {
		t.Fatalf("CRLF source should be accepted: %v", err)
	}
}

// 注释行（#）不参与元数据，也不占用"字段重复"的判定。
func TestParseSkipsComments(t *testing.T) {
	source := "# 这是说明\n名称：数学解题引导\n适用条件：方程\n启用：是\n\n引导规程。\n"
	definition, err := Parse(source)
	if err != nil {
		t.Fatalf("comment line should be skipped: %v", err)
	}
	if definition.Name != "数学解题引导" {
		t.Fatalf("name = %q", definition.Name)
	}
}

func TestSplitListHandlesEverySeparator(t *testing.T) {
	got := SplitList(" 方程,去括号，分数、通分;面积；应用题|未知数｜行程 , ")
	want := []string{"方程", "去括号", "分数", "通分", "面积", "应用题", "未知数", "行程"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("element %d = %q, want %q", index, got[index], want[index])
		}
	}
	if len(SplitList("  ")) != 0 {
		t.Fatal("blank input must yield no items")
	}
}
