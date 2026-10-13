package chat

import (
	"strings"
	"testing"

	"campusclaw/backend/internal/rag"
	"campusclaw/backend/internal/skill"
)

func hit(materialID uint64, title, text string) rag.Hit {
	return rag.Hit{
		ChunkID: materialID * 10, MaterialID: materialID, Title: title,
		ChunkIndex: 1, StartOffset: 10, EndOffset: 20, Text: text,
	}
}

// 消息顺序固定为 系统 → 历史 → 本轮提问；资料与提问同放在最后那条用户消息里。
func TestBuildMessagesKeepsFixedOrder(t *testing.T) {
	messages, citations := BuildMessages(BuildInput{
		AgentPrompt: "你是数学助教。",
		Skill:       skill.Row{ID: 1, Name: "数学解题引导", Description: "只提示下一步。"},
		HasSkill:    true,
		Hits:        []rag.Hit{hit(5, "数学自学笔记.md", "去括号时要把括号外的数乘到每一项。")},
		Question:    "去括号要注意什么？",
		History: []Message{
			{ID: 1, Role: "user", Content: "上一轮的问题"},
			{ID: 2, Role: "assistant", Content: "上一轮的回答"},
		},
	})
	if len(messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(messages))
	}
	if messages[0].Role != "system" {
		t.Fatalf("first message role = %q", messages[0].Role)
	}
	if messages[1].Role != "user" || messages[2].Role != "assistant" {
		t.Fatalf("history roles = %q, %q", messages[1].Role, messages[2].Role)
	}
	last := messages[3]
	if last.Role != "user" {
		t.Fatalf("last message role = %q", last.Role)
	}
	if !strings.Contains(last.Content, "资料：") || !strings.Contains(last.Content, "问题：去括号要注意什么？") {
		t.Fatalf("last message = %q", last.Content)
	}
	// 资料在用户消息里，不在系统消息里。系统消息只承担角色与约束。
	if strings.Contains(messages[0].Content, "去括号时要把括号外的数乘到每一项") {
		t.Fatal("material must not be injected into the system message")
	}
	if len(citations) != 1 || citations[0].MaterialID != 5 || citations[0].ChunkID != 50 {
		t.Fatalf("citations = %+v", citations)
	}
}

// 提示词留空时回退到默认角色设定，而不是让系统消息缺一段。
func TestBuildMessagesFallsBackToDefaultPrompt(t *testing.T) {
	messages, _ := BuildMessages(BuildInput{Question: "你好"})
	if !strings.HasPrefix(messages[0].Content, DefaultAgentPrompt) {
		t.Fatalf("system message = %q", messages[0].Content)
	}
}

// 命中"解题引导"类技能时追加不许给最终答案的硬性要求。
func TestBuildMessagesGuidesInsteadOfAnswering(t *testing.T) {
	messages, _ := BuildMessages(BuildInput{
		Skill:    skill.Row{ID: 1, Name: "数学解题引导", Description: "只提示下一步。"},
		HasSkill: true,
		Question: "3x+6=12，x 是多少？",
	})
	if !strings.Contains(messages[0].Content, "不要写出最终答案") {
		t.Fatalf("guidance requirement missing: %q", messages[0].Content)
	}

	plain, _ := BuildMessages(BuildInput{
		Skill:    skill.Row{ID: 2, Name: "概念讲解", Description: "解释概念。"},
		HasSkill: true,
		Question: "什么是方程？",
	})
	if strings.Contains(plain[0].Content, "不要写出最终答案") {
		t.Fatal("non-guidance skill must not carry the answer-withholding rule")
	}
}

// 没有命中资料时明确告知"本次没有资料"，避免模型自行编造出处。
func TestBuildMessagesStatesWhenThereIsNoMaterial(t *testing.T) {
	messages, citations := BuildMessages(BuildInput{Question: "没有资料的问题"})
	if len(citations) != 0 {
		t.Fatalf("citations = %+v", citations)
	}
	if !strings.Contains(messages[0].Content, "本次没有资料") {
		t.Fatalf("system message = %q", messages[0].Content)
	}
	if strings.Contains(messages[len(messages)-1].Content, "资料：") {
		t.Fatal("user message must not claim to carry material")
	}
}

// 空正文的切片被剔除后重新编号：资料里的 [n] 必须与 citations[n-1] 严格对齐。
func TestBuildMessagesRenumbersAfterDroppingBlankHits(t *testing.T) {
	messages, citations := BuildMessages(BuildInput{
		Hits: []rag.Hit{
			hit(1, "空切片.md", "   "),
			hit(2, "有效切片.md", "这是有效内容。"),
			hit(3, "另一个有效切片.md", "另一段有效内容。"),
		},
		Question: "哪段有内容？",
	})
	if len(citations) != 2 {
		t.Fatalf("citations = %+v", citations)
	}
	if citations[0].MaterialID != 2 || citations[1].MaterialID != 3 {
		t.Fatalf("citations out of order: %+v", citations)
	}
	body := messages[len(messages)-1].Content
	if !strings.Contains(body, "[1] 材料《有效切片.md》") {
		t.Fatalf("first kept hit should be numbered [1]: %q", body)
	}
	if !strings.Contains(body, "[2] 材料《另一个有效切片.md》") {
		t.Fatalf("second kept hit should be numbered [2]: %q", body)
	}
	if strings.Contains(body, "[3]") {
		t.Fatalf("numbering must restart after dropping blanks: %q", body)
	}
}

// 超长历史被截断到上限，避免把上下文窗口撑爆。
func TestBuildMessagesTruncatesLongMaterial(t *testing.T) {
	long := strings.Repeat("字", MaterialRunes+50)
	messages, _ := BuildMessages(BuildInput{
		Hits:     []rag.Hit{hit(1, "长材料.md", long)},
		Question: "总结一下",
	})
	body := messages[len(messages)-1].Content
	if strings.Count(body, "字") != MaterialRunes {
		t.Fatalf("material not truncated to %d runes: %d", MaterialRunes, strings.Count(body, "字"))
	}
}

// 无依据分支：正文里出现的 [n] 一律剥掉，不留指向不存在条目的编号。
func TestStripCitationsRemovesNumbering(t *testing.T) {
	got := StripCitations("磁铁不能吸木块[1]，但能吸铁钉 [2]。")
	if strings.Contains(got, "[") || strings.Contains(got, "]") {
		t.Fatalf("citations not stripped: %q", got)
	}
}

// 有依据时过滤器原样放行；无依据时逐段剥编号。
func TestCitationFilterPassesThroughWhenGroundingExists(t *testing.T) {
	filter := FilterFor(true)
	if got := filter.Push("依据如下[1]。"); got != "依据如下[1]。" {
		t.Fatalf("grounded output must pass through: %q", got)
	}
	if tail := filter.Flush(); tail != "" {
		t.Fatalf("flush = %q, want empty", tail)
	}
}

// 编号可能被拆到两帧之间：前半段不能先漏出去。
func TestCitationFilterHoldsSplitCitation(t *testing.T) {
	filter := FilterFor(false)
	if got := filter.Push("答案是不行[1"); got != "答案是不行" {
		t.Fatalf("prefix should be held back: %q", got)
	}
	if got := filter.Push("]。"); got != "。" {
		t.Fatalf("held prefix should be dropped: %q", got)
	}
}

// 暂存的内容若最终不是编号（例如普通的方括号），必须在收尾时补回。
func TestCitationFilterReleasesNonCitationBrackets(t *testing.T) {
	filter := FilterFor(false)
	first := filter.Push("见附录[")
	if first != "见附录" {
		t.Fatalf("open bracket should be held: %q", first)
	}
	second := filter.Push("甲")
	if second != "[甲" {
		t.Fatalf("non-digit tail should be released with the bracket: %q", second)
	}
}

// 结尾暂存的编号在收尾时被丢弃，而不是漏给浏览器。
func TestCitationFilterDropsTrailingCitation(t *testing.T) {
	filter := FilterFor(false)
	_ = filter.Push("这是结论[3]")
	if tail := filter.Flush(); strings.Contains(tail, "[") {
		t.Fatalf("flush leaked a citation: %q", tail)
	}
}

func TestTitleForUsesQuestionPrefix(t *testing.T) {
	if got := TitleFor("  磁铁能吸木块吗？为什么？  "); got != "磁铁能吸木块吗？为什么？" {
		t.Fatalf("title = %q", got)
	}
	long := strings.Repeat("长", TitleRunes+10)
	if got := TitleFor(long); len([]rune(got)) != TitleRunes {
		t.Fatalf("title length = %d, want %d", len([]rune(got)), TitleRunes)
	}
	if got := TitleFor("   "); got != PlaceholderTitle {
		t.Fatalf("blank question title = %q", got)
	}
}
