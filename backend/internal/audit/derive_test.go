package audit_test

import (
	"strings"
	"testing"

	"campusclaw/backend/internal/audit"
	"campusclaw/backend/internal/chat"
	"campusclaw/backend/internal/rag"
	"campusclaw/backend/internal/skill"
)

// 派生列必须与真正发出去的消息一致：系统消息 = messages[0]，
// 用户消息 = 最后一条 user，注入字符数 = 「资料：」区块 + 工具结果。
//
// 这个测试同时锁住 audit 与 chat 之间那个隐含约定（资料区块的边界标记），
// 两边任何一处改动都会让它红。
func TestDeriveMatchesBuiltMessages(t *testing.T) {
	hits := []rag.Hit{
		{MaterialID: 1, Title: "第一章.pdf", ChunkIndex: 0, StartOffset: 0, EndOffset: 12, Text: "一元二次方程的求根公式。"},
		{MaterialID: 1, Title: "第一章.pdf", ChunkIndex: 1, StartOffset: 13, EndOffset: 30, Text: "判别式决定实根个数。"},
	}
	messages, citations := chat.BuildMessages(chat.BuildInput{
		AgentPrompt: "你是{{app_name}}的助教。",
		Skill:       skill.Row{Name: "数学解题引导", Description: "先看学生写到哪一步。"},
		HasSkill:    true,
		Hits:        hits,
		Question:    "判别式怎么用",
	})
	if len(citations) != 2 {
		t.Fatalf("citations = %d, want 2", len(citations))
	}
	plain := make([]audit.Message, 0, len(messages))
	for _, message := range messages {
		plain = append(plain, audit.Message{Role: message.Role, Content: message.Content})
	}
	system, user, contextChars := audit.Derive(plain)

	if !strings.HasPrefix(system, "你是{{app_name}}的助教。") {
		t.Fatalf("系统消息未取到首段：%q", system)
	}
	if !strings.Contains(system, "当前技能：数学解题引导") {
		t.Fatalf("系统消息缺少技能段：%q", system)
	}
	if !strings.HasSuffix(strings.TrimSpace(user), "问题：判别式怎么用") {
		t.Fatalf("用户消息未以提问结尾：%q", user)
	}
	// 资料区块的字符数必须能被独立算回来：两段正文 + 各自的编号行。
	wantBlock := len([]rune(user[strings.Index(user, "资料：\n")+len("资料：\n") : strings.LastIndex(user, "\n\n问题：")]))
	if contextChars != wantBlock {
		t.Fatalf("注入字符数 = %d, want %d", contextChars, wantBlock)
	}
	if contextChars <= 0 || contextChars >= len([]rune(user)) {
		t.Fatalf("注入字符数应小于整条用户消息：%d vs %d", contextChars, len([]rune(user)))
	}
}

// 工具结果也算注入内容：它和资料一样是"这次带给模型的外部文本"。
func TestDeriveCountsToolMessages(t *testing.T) {
	messages := []audit.Message{
		{Role: "system", Content: "系统"},
		{Role: "user", Content: "问题：换算一下"},
		{Role: "assistant", Content: ""},
		{Role: "tool", Content: "3.2 公里 = 3200 米"},
		{Role: "user", Content: "再算一个"},
	}
	_, _, contextChars := audit.Derive(messages)
	if want := len([]rune("3.2 公里 = 3200 米")); contextChars != want {
		t.Fatalf("注入字符数 = %d, want %d", contextChars, want)
	}
}

// 请求体里的 messages 能被解析回来，用于核对"存的与发的一致"。
func TestSniffMessages(t *testing.T) {
	request := []byte(`{"model":"course-chat","messages":[{"role":"system","content":"s"},{"role":"user","content":"u"}],"stream":true}`)
	messages, err := audit.SniffMessages(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 || messages[1].Content != "u" {
		t.Fatalf("messages = %+v", messages)
	}
}
