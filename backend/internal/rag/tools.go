package rag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ToolDecl 是一条发给模型的工具声明。三个字段全部来自 MCP 侧的发现结果：
// Parameters 就是远端 tools/list 里的 inputSchema 原文，不重排、不补默认值。
type ToolDecl struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall 是模型提议的一次调用。Arguments 是未解析的 JSON 原文，
// 由宿主按声明里的 inputSchema 校验后才允许执行。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ChatRequest 是一次对话请求的全部输入。Tools 为空时请求体里不会出现 tools 字段。
type ChatRequest struct {
	Messages    []ChatMessage
	Tools       []ToolDecl
	ToolChoice  string
	Temperature float64
	MaxTokens   int
}

// ChatReply 是一次调用的结果，同时保留"真正发出的字节"与上游状态，供审计使用。
type ChatReply struct {
	Content          string
	FinishReason     string
	ToolCalls        []ToolCall
	RequestJSON      []byte
	HTTPStatus       int
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
}

// ChatStreamTools 是带工具声明的流式调用。
//
// 三点实现细节值得记住：
//   - 上游会先推大量 reasoning_content（模型思考），一律丢弃：既不转发也不计入正文；
//   - tool_calls 按 index 增量给出：首帧带 id 与 name，后续帧只追加 arguments 片段，必须拼装；
//   - usage 在最后一帧（choices 为空）里给出，需要 stream_options.include_usage 才返回。
func (g *Gateway) ChatStreamTools(ctx context.Context, request ChatRequest, onDelta func(string) error) (ChatReply, error) {
	reply := ChatReply{}
	if g == nil || !g.ChatEnabled() {
		return reply, ErrDependency
	}
	temperature := request.Temperature
	if temperature == 0 {
		temperature = defaultStreamTemperature
	}
	maxTokens := request.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultStreamMaxTokens
	}
	payload := map[string]any{
		"model":          g.config.ChatModel,
		"messages":       encodeMessages(request.Messages),
		"temperature":    temperature,
		"max_tokens":     maxTokens,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	// 没有候选工具时整体省略这两项——不是空数组。第 5 课的请求体因此逐字节不变。
	if len(request.Tools) > 0 {
		payload["tools"] = encodeTools(request.Tools)
		choice := request.ToolChoice
		if choice == "" {
			choice = "auto"
		}
		payload["tool_choice"] = choice
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return reply, fmt.Errorf("%w: encode request", ErrDependency)
	}
	reply.RequestJSON = body
	endpoint := strings.TrimRight(g.config.ChatBaseURL, "/") + "/chat/completions"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return reply, fmt.Errorf("%w: build request", ErrDependency)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	if g.config.ChatAPIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+g.config.ChatAPIKey)
	}
	started := time.Now()
	response, err := g.stream.Do(httpRequest)
	reply.Latency = time.Since(started)
	if err != nil {
		// 只记耗时与错误类型：请求体里有提示词与资料，不进日志。
		log.Printf("chat gateway: request failed after %s: %v", reply.Latency.Round(time.Millisecond), err)
		return reply, fmt.Errorf("%w: %v", ErrDependency, err)
	}
	defer response.Body.Close()
	reply.HTTPStatus = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode > 299 {
		// 上游错误正文可能含密钥或提示词，只读丢弃，不进入返回值与日志。
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		return reply, fmt.Errorf("%w: status %d", ErrDependency, response.StatusCode)
	}
	if err := g.consumeStream(ctx, response.Body, &reply, onDelta); err != nil {
		log.Printf("chat gateway: stream failed after %s (status %d, finish %q): %v",
			reply.Latency.Round(time.Millisecond), reply.HTTPStatus, reply.FinishReason, err)
		return reply, err
	}
	reply.Latency = time.Since(started)
	return reply, nil
}

func encodeMessages(messages []ChatMessage) []map[string]any {
	encoded := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := map[string]any{"role": message.Role, "content": message.Content}
		if len(message.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				calls = append(calls, map[string]any{
					"id": call.ID, "type": "function",
					"function": map[string]string{"name": call.Name, "arguments": call.Arguments},
				})
			}
			item["tool_calls"] = calls
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if message.Name != "" {
			item["name"] = message.Name
		}
		encoded = append(encoded, item)
	}
	return encoded
}

func encodeTools(tools []ToolDecl) []map[string]any {
	encoded := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		encoded = append(encoded, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": parameters,
			},
		})
	}
	return encoded
}

// accumulator 按 index 拼装流式 tool_calls：首帧给 id 与 name，之后只有 arguments 片段。
type accumulator struct {
	order []int
	items map[int]*ToolCall
}

func newAccumulator() *accumulator {
	return &accumulator{items: map[int]*ToolCall{}}
}

func (a *accumulator) absorb(fragments []toolCallFragment) {
	for _, fragment := range fragments {
		item, ok := a.items[fragment.Index]
		if !ok {
			item = &ToolCall{}
			a.items[fragment.Index] = item
			a.order = append(a.order, fragment.Index)
		}
		if fragment.ID != "" {
			item.ID = fragment.ID
		}
		if fragment.Function.Name != "" {
			item.Name = fragment.Function.Name
		}
		item.Arguments += fragment.Function.Arguments
	}
}

func (a *accumulator) list() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	sort.Ints(a.order)
	calls := make([]ToolCall, 0, len(a.order))
	for _, index := range a.order {
		calls = append(calls, *a.items[index])
	}
	return calls
}

type toolCallFragment struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (g *Gateway) consumeStream(ctx context.Context, body io.Reader, reply *ChatReply, onDelta func(string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamLineLimit)
	var answer strings.Builder
	calls := newAccumulator()
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
					Content          string             `json:"content"`
					ReasoningContent string             `json:"reasoning_content"`
					ToolCalls        []toolCallFragment `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			continue
		}
		if frame.Usage != nil {
			reply.PromptTokens = frame.Usage.PromptTokens
			reply.CompletionTokens = frame.Usage.CompletionTokens
		}
		if len(frame.Choices) == 0 {
			continue
		}
		choice := frame.Choices[0]
		// reasoning_content 是模型思考，既不给前端，也不算正文。
		if len(choice.Delta.ToolCalls) > 0 {
			calls.absorb(choice.Delta.ToolCalls)
		}
		if choice.FinishReason != "" {
			reply.FinishReason = choice.FinishReason
		}
		text := choice.Delta.Content
		if text == "" {
			continue
		}
		answer.WriteString(text)
		if onDelta != nil {
			if err := onDelta(text); err != nil {
				reply.Content = answer.String()
				return err
			}
		}
	}
	reply.Content = answer.String()
	reply.ToolCalls = calls.list()
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%w: read stream: %v", ErrDependency, err)
	}
	return nil
}

// AnswerStreamTools 是给编排层用的入口：一次调用，带不带工具都可以。
func (s *Service) AnswerStreamTools(ctx context.Context, request ChatRequest, onDelta func(string) error) (ChatReply, error) {
	if s == nil || !s.ChatEnabled() {
		return ChatReply{}, ErrDependency
	}
	return s.gateway.ChatStreamTools(ctx, request, onDelta)
}
