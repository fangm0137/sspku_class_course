package rag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatMessage 是一条对话消息。系统消息与用户消息都由调用方组装，
// 本层只负责运输，不决定消息内容。
type ChatMessage struct {
	Role    string
	Content string
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

// ChatStream 调用对话网关的流式接口，每收到一段就回调 onDelta。
// 返回完整回答（由各段顺序拼接）。上游非 2xx、读流失败或一段都没产出时归为 ErrDependency；
// onDelta 返回的错误原样上抛，供调用方中断（例如客户端断开）。
func (g *Gateway) ChatStream(ctx context.Context, messages []ChatMessage, options StreamOptions, onDelta func(string) error) (string, error) {
	if g == nil || !g.ChatEnabled() {
		return "", ErrDependency
	}
	if options.Temperature == 0 {
		options.Temperature = defaultStreamTemperature
	}
	if options.MaxTokens == 0 {
		options.MaxTokens = defaultStreamMaxTokens
	}
	encoded := make([]map[string]string, len(messages))
	for index, message := range messages {
		encoded[index] = map[string]string{"role": message.Role, "content": message.Content}
	}
	body, err := json.Marshal(map[string]any{
		"model":       g.config.ChatModel,
		"messages":    encoded,
		"temperature": options.Temperature,
		"max_tokens":  options.MaxTokens,
		"stream":      true,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode request", ErrDependency)
	}
	endpoint := strings.TrimRight(g.config.ChatBaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: build request", ErrDependency)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if g.config.ChatAPIKey != "" {
		request.Header.Set("Authorization", "Bearer "+g.config.ChatAPIKey)
	}
	response, err := g.stream.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDependency, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		// 上游错误正文可能含密钥或提示词，只读丢弃，不进入返回值与日志。
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		return "", fmt.Errorf("%w: status %d", ErrDependency, response.StatusCode)
	}
	return g.readStream(response.Body, onDelta)
}

// readStream 解析 OpenAI 兼容的 SSE 帧：逐行读取，data: 之后是 JSON，
// [DONE] 表示结束；无法解析的空帧跳过，不影响整段生成。
func (g *Gateway) readStream(body io.Reader, onDelta func(string) error) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamLineLimit)
	var answer strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil || len(frame.Choices) == 0 {
			continue
		}
		text := frame.Choices[0].Delta.Content
		if text == "" {
			continue
		}
		answer.WriteString(text)
		if onDelta != nil {
			if err := onDelta(text); err != nil {
				return answer.String(), err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return answer.String(), fmt.Errorf("%w: read stream", ErrDependency)
	}
	if answer.Len() == 0 {
		return "", fmt.Errorf("%w: empty completion", ErrDependency)
	}
	return answer.String(), nil
}

// StreamTimeout 是流式生成的整体上限，由调用方放进请求上下文。
const StreamTimeout = 120 * time.Second

// AnswerStream 是给上层编排用的入口：把已组装好的消息交给对话网关做流式生成。
// 这样"谁持有 HTTP 客户端与密钥"始终只有 rag 一处。
func (s *Service) AnswerStream(ctx context.Context, messages []ChatMessage, options StreamOptions, onDelta func(string) error) (string, error) {
	if s == nil || !s.ChatEnabled() {
		return "", ErrDependency
	}
	return s.gateway.ChatStream(ctx, messages, options, onDelta)
}
