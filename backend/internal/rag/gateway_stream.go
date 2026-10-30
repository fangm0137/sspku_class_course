package rag

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ChatMessage 是一条对话消息。系统消息与用户消息都由调用方组装，
// 本层只负责运输，不决定消息内容。
//
// 三个附加字段只服务于工具循环：assistant 消息带回它提议的调用，
// role:tool 消息带回 tool_call_id 与工具名，其余场景都留空。
type ChatMessage struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// StreamOptions 是流式生成的参数。零值即服务端默认。
type StreamOptions struct {
	Temperature float64
	MaxTokens   int
}

const (
	defaultStreamTemperature = 0.2
	defaultStreamMaxTokens   = 800
	streamLineLimit          = 1 << 20
)

// ChatStream 是不带工具声明的流式生成，第 3–5 课的调用路径走这里。
// 上游非 2xx、读流失败或一段都没产出时归为 ErrDependency；
// onDelta 返回的错误原样上抛，供调用方中断（例如客户端断开）。
func (g *Gateway) ChatStream(ctx context.Context, messages []ChatMessage, options StreamOptions, onDelta func(string) error) (string, error) {
	reply, err := g.ChatStreamTools(ctx, ChatRequest{
		Messages: messages, Temperature: options.Temperature, MaxTokens: options.MaxTokens,
	}, onDelta)
	if err != nil {
		return reply.Content, err
	}
	if strings.TrimSpace(reply.Content) == "" {
		return "", fmt.Errorf("%w: empty completion", ErrDependency)
	}
	return reply.Content, nil
}

// StreamTimeout 是流式生成的整体上限，由调用方放进请求上下文。
// 工具循环可能包含多轮，因此这个上限覆盖整次提问而不是单轮。
const StreamTimeout = 150 * time.Second

// AnswerStream 是给上层编排用的入口：把已组装好的消息交给对话网关做流式生成。
// 这样"谁持有 HTTP 客户端与密钥"始终只有 rag 一处。
func (s *Service) AnswerStream(ctx context.Context, messages []ChatMessage, options StreamOptions, onDelta func(string) error) (string, error) {
	if s == nil || !s.ChatEnabled() {
		return "", ErrDependency
	}
	return s.gateway.ChatStream(ctx, messages, options, onDelta)
}
