package chat

import (
	"context"
	"encoding/json"
	"log"

	"campusclaw/backend/internal/audit"
	"campusclaw/backend/internal/rag"
)

// recordCall 写入一轮调用的审计。返回值是本轮编号，供 tool_calls 回指。
//
// 这里刻意不让审计失败向上传播：审计表坏了不该变成"问答不可用"。
// 但失败的调用同样写一行（status=failed），否则故障发生时反而没有证据。
func (s *Service) recordCall(ctx context.Context, prepared Prepared, round int, request rag.ChatRequest,
	reply rag.ChatReply, callErr error) uint64 {
	if s.audit == nil {
		return 0
	}
	messages := make([]audit.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, audit.Message{Role: message.Role, Content: message.Content})
	}
	call := audit.Call{
		ClassID:          prepared.ClassID,
		ConversationID:   prepared.Conversation.ID,
		UserID:           prepared.UserID,
		AgentID:          prepared.Agent.ID,
		RoundNo:          round,
		Model:            s.modelName(),
		RequestJSON:      reply.RequestJSON,
		Messages:         messages,
		SkillName:        skillNameOf(prepared),
		ToolDefCount:     len(request.Tools),
		ToolCallCount:    len(reply.ToolCalls),
		FinishReason:     reply.FinishReason,
		ResponseText:     reply.Content,
		ResponseJSON:     responseSummary(reply),
		HTTPStatus:       reply.HTTPStatus,
		OK:               callErr == nil,
		PromptTokens:     reply.PromptTokens,
		CompletionTokens: reply.CompletionTokens,
		LatencyMS:        int(reply.Latency.Milliseconds()),
	}
	if callErr != nil {
		call.ErrorText = callErr.Error()
	}
	id, err := s.audit.RecordCall(ctx, call)
	if err != nil {
		log.Printf("audit: llm call record failed: %v", err)
		return 0
	}
	return id
}

func (s *Service) modelName() string {
	if s.retrieval == nil {
		return ""
	}
	return s.retrieval.ChatModel()
}

func skillNameOf(prepared Prepared) string {
	if prepared.HasSkill {
		return prepared.Skill.Name
	}
	return ""
}

// responseSummary 把响应压成一份可读摘要。请求体是逐字节原文（那是证据的主干），
// 响应这边保存"正文 + 结束原因 + 提议的调用"，足够回答"拿回了什么"。
func responseSummary(reply rag.ChatReply) string {
	type call struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	summary := struct {
		Content      string `json:"content"`
		FinishReason string `json:"finish_reason"`
		ToolCalls    []call `json:"tool_calls,omitempty"`
	}{Content: reply.Content, FinishReason: reply.FinishReason}
	for _, item := range reply.ToolCalls {
		summary.ToolCalls = append(summary.ToolCalls, call{Name: item.Name, Arguments: item.Arguments})
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return ""
	}
	return string(encoded)
}
