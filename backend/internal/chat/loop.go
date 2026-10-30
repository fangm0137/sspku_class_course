package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"campusclaw/backend/internal/audit"
	"campusclaw/backend/internal/mcp"
	"campusclaw/backend/internal/rag"
)

// 单条工具结果的截断上限，与资料切片同口径。
const toolOutputRunes = 800

// ToolRecord 是一次工具调用的对外摘要，供 SSE 的 meta.tool_calls 与前端工具栏使用。
type ToolRecord struct {
	Ref       string `json:"ref"`
	Status    string `json:"status"`
	LatencyMS int    `json:"latency_ms"`
	Text      string `json:"text"`
}

// Hooks 是编排层向外推送的两个出口：正文增量与工具调用摘要。
type Hooks struct {
	Delta func(string) error
	Tool  func(ToolRecord)
}

// StreamResult 是一轮提问的结果。
type StreamResult struct {
	MessageID uint64
	Answer    string
	Partial   bool
}

// Stream 执行生成：按需循环调用工具，每段增量先经编号过滤器再交给 hooks.Delta，
// 最后把助手消息（连同引用与技能名）落库。
//
// 循环的终止条件有三个：模型不再提出调用、达到轮次上限、达到单轮次数上限。
// 达到上限不是错误：下一轮不再带 tools，让模型用已有信息作答。
func (s *Service) Stream(ctx context.Context, prepared Prepared, hooks Hooks) (StreamResult, error) {
	if s.retrieval == nil {
		return StreamResult{}, rag.ErrDependency
	}
	filter := FilterFor(len(prepared.Citations) > 0)
	var answer strings.Builder
	var streamErr error
	messages := prepared.Messages
	rounds := s.settings.MaxRounds
	if rounds < 1 {
		rounds = 1
	}
	// 多留一轮：工具轮用尽后还要有一次"不带工具"的收尾请求，否则模型没有机会作答。
	for round := 1; round <= rounds+1; round++ {
		allowTools := len(prepared.Declarations) > 0 && round <= rounds
		request := rag.ChatRequest{Messages: messages}
		if allowTools {
			request.Tools = toolDeclarations(prepared.Declarations)
			request.ToolChoice = "auto"
		}
		reply, callErr := s.retrieval.AnswerStreamTools(ctx, request, func(delta string) error {
			text := filter.Push(delta)
			if text == "" {
				return nil
			}
			answer.WriteString(text)
			return hooks.Delta(text)
		})
		// 审计是旁路：写失败只记日志，不影响本次回答。
		callID := s.recordCall(ctx, prepared, round, request, reply, callErr)
		if callErr != nil {
			streamErr = callErr
			break
		}
		if !allowTools || reply.FinishReason != "tool_calls" || len(reply.ToolCalls) == 0 {
			break
		}
		assistant, toolMessages := s.runTools(ctx, prepared, callID, round, reply, hooks)
		messages = append(messages, assistant)
		messages = append(messages, toolMessages...)
	}
	if tail := filter.Flush(); tail != "" {
		answer.WriteString(tail)
		if emitErr := hooks.Delta(tail); emitErr != nil && streamErr == nil {
			streamErr = emitErr
		}
	}
	partial := answer.Len() > 0
	if streamErr != nil && !partial {
		// 一段都没产出：把失败如实上报给调用方（流已开始则由它以 error 事件收尾）。
		return StreamResult{}, streamErr
	}
	skillName := ""
	if prepared.HasSkill {
		skillName = prepared.Skill.Name
	}
	id, persistErr := s.chats.FinishTurn(ctx, prepared.Conversation.ID, answer.String(),
		encodeCitations(prepared.Citations), skillName)
	if persistErr != nil {
		return StreamResult{Answer: answer.String(), Partial: partial}, persistErr
	}
	return StreamResult{MessageID: id, Answer: answer.String(), Partial: partial}, nil
}

// runTools 执行模型在一轮里提出的全部调用，返回 assistant 消息与对应的 tool 消息。
// 次数上限之外的那些调用不发出去，但同样要给出 tool 消息——否则消息序列不完整，模型会一直等。
func (s *Service) runTools(ctx context.Context, prepared Prepared, callID uint64, round int,
	reply rag.ChatReply, hooks Hooks) (rag.ChatMessage, []rag.ChatMessage) {
	assistant := rag.ChatMessage{Role: "assistant", Content: reply.Content, ToolCalls: reply.ToolCalls}
	limit := s.settings.MaxCalls
	if limit < 1 {
		limit = 1
	}
	messages := make([]rag.ChatMessage, 0, len(reply.ToolCalls))
	for index, call := range reply.ToolCalls {
		var outcome toolOutcome
		if index < limit {
			outcome = s.executeOne(ctx, prepared, call)
		} else {
			outcome = toolOutcome{
				status: audit.ToolDenied, text: fmt.Sprintf("本次提问的调用次数已达到上限（%d 次），这次没有执行。", limit),
				ref: call.Name, errorText: "超出单轮调用次数上限",
			}
			// 被配额拦下的调用仍要记清"本来要调哪台服务器的哪个工具"，
			// 否则审计里只留一个空服务器名，事后没法判断它是否本该被允许。
			if declaration, found := findDeclaration(prepared.Declarations, call.Name); found {
				outcome.ref = declaration.Ref
				outcome.serverID = declaration.ServerID
				outcome.serverName = declaration.ServerName
			}
		}
		if s.audit != nil {
			_ = s.audit.RecordToolCall(ctx, audit.ToolRow{
				ClassID: prepared.ClassID, ConversationID: prepared.Conversation.ID, UserID: prepared.UserID,
				LLMCallID: callID, RoundNo: round, ServerID: outcome.serverID, ServerName: outcome.serverName,
				ToolName: call.Name, ArgumentsJSON: argumentsOf(call), Status: outcome.status,
				ResultExcerpt: outcome.excerpt, ErrorText: outcome.errorText, LatencyMS: outcome.latencyMS,
			})
		}
		if hooks.Tool != nil {
			hooks.Tool(ToolRecord{Ref: outcome.ref, Status: outcome.status, LatencyMS: outcome.latencyMS, Text: outcome.text})
		}
		messages = append(messages, rag.ChatMessage{
			Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: outcome.text,
		})
	}
	return assistant, messages
}

// toolOutcome 是一次调用的内部结论：给模型看的文本 + 给审计看的三态。
type toolOutcome struct {
	text       string
	status     string
	latencyMS  int
	ref        string
	serverID   uint64
	serverName string
	excerpt    string
	errorText  string
}

// executeOne 走完"授权 → 校验 → 调用 → 清洗"四步。任何一步不通过都不发远端请求，
// 但一定要给出 tool 消息，让模型知道发生了什么。
func (s *Service) executeOne(ctx context.Context, prepared Prepared, call rag.ToolCall) toolOutcome {
	declaration, found := findDeclaration(prepared.Declarations, call.Name)
	if !found {
		return toolOutcome{
			status: audit.ToolDenied, ref: call.Name, text: "工具 " + call.Name + " 不在本轮候选集内，未执行。",
			errorText: "工具不在候选集内",
		}
	}
	outcome := toolOutcome{ref: declaration.Ref, serverID: declaration.ServerID, serverName: declaration.ServerName}
	// 参数校验用的是声明里的同一份 inputSchema（即发现结果原文）。
	if err := mcp.ValidateArguments(declaration.Parameters, []byte(argumentsOf(call))); err != nil {
		outcome.status = audit.ToolError
		outcome.text = "参数校验未通过，未调用远端工具：" + err.Error()
		outcome.errorText = err.Error()
		return outcome
	}
	if s.servers == nil {
		outcome.status = audit.ToolDenied
		outcome.text = "外部工具当前不可用，未执行。"
		outcome.errorText = "MCP 未启用"
		return outcome
	}
	baseURL, token, connected, err := s.servers.Target(ctx, prepared.ClassID, declaration.ServerID)
	if err != nil {
		outcome.status = audit.ToolDenied
		outcome.text = "工具对应的服务器已不可用，未执行。"
		outcome.errorText = "服务器读取失败"
		return outcome
	}
	// 授权逐工具判断：服务器断开即拒绝，哪怕它在保存技能时是连通的。
	if !connected {
		outcome.status = audit.ToolDenied
		outcome.text = "工具对应的服务器已断开，本次调用未执行。"
		outcome.errorText = "服务器未连接"
		return outcome
	}
	client, err := s.servers.NewClientFor(baseURL, token)
	if err != nil {
		outcome.status = audit.ToolError
		outcome.text = "工具地址无效，未执行。"
		outcome.errorText = "地址无效"
		return outcome
	}
	callCtx, cancel := context.WithTimeout(ctx, s.settings.CallTimeout)
	defer cancel()
	started := time.Now()
	result, callErr := client.Call(callCtx, declaration.Name, []byte(argumentsOf(call)))
	outcome.latencyMS = int(time.Since(started).Milliseconds())
	if callErr != nil {
		outcome.status = audit.ToolError
		outcome.text = "工具调用失败：" + callErr.Error()
		outcome.errorText = callErr.Error()
		return outcome
	}
	if result.IsError {
		outcome.status = audit.ToolError
		outcome.text = "工具返回了错误结果，未采信。"
		outcome.errorText = "远端 isError"
		return outcome
	}
	// 工具输出按不可信文本处理：命中指令式句式就整条丢弃，不做单句删除。
	screened := screenToolOutput(strings.Join(result.Content, "\n"))
	outcome.status = audit.ToolOK
	outcome.text = truncateRunes(screened, toolOutputRunes)
	outcome.excerpt = truncateRunes(screened, 200)
	return outcome
}

func findDeclaration(declarations []mcp.Declaration, name string) (mcp.Declaration, bool) {
	for _, declaration := range declarations {
		if declaration.Name == name {
			return declaration, true
		}
	}
	return mcp.Declaration{}, false
}

// argumentsOf 返回模型给出的参数原文；空串按空对象处理，保证是一条合法 JSON。
func argumentsOf(call rag.ToolCall) string {
	if strings.TrimSpace(call.Arguments) == "" {
		return "{}"
	}
	return call.Arguments
}
