package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"campusclaw/backend/internal/agent"
	"campusclaw/backend/internal/chat"
	"campusclaw/backend/internal/rag"
)

// answerHandler 提供一次流式回答：POST /api/conversations/{id}/messages。
// 事件顺序固定为 meta（先带引用与本次的工具调用）→ delta × n → done。
type answerHandler struct {
	service *chat.Service
}

type answerWrite struct {
	Question string `json:"question"`
}

func (h answerHandler) stream(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	var input answerWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 不支持流式刷新时不能假装在流式推送，直接按服务错误处理。
		apiError(w, 500, "internal_error", "服务内部错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rag.StreamTimeout)
	defer cancel()
	// 检索前即可判定的失败全部走状态码：这之后响应头已经发出，状态码不可再变更。
	prepared, err := h.service.Prepare(ctx, user.ClassID, user.ID, id, input.Question)
	if err != nil {
		writeAnswerError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// meta 延迟到"确定要发正文"之前才发出：工具调用是在流里才发现的，
	// 若一开始就发 meta，就没有机会把 tool_calls 一起带上。
	// 这样仍然满足"tool_calls 先于首个 delta"，且无调用时该字段整体省略。
	var (
		metaSent  bool
		toolCalls []chat.ToolRecord
	)
	var selected any
	if prepared.HasSkill {
		selected = map[string]string{"name": prepared.Skill.Name}
	}
	emitMeta := func() error {
		if metaSent {
			return nil
		}
		metaSent = true
		meta := map[string]any{
			"status":          "ok",
			"conversation_id": prepared.Conversation.ID,
			"user_message_id": prepared.UserMessageID,
			"skill":           selected,
			"citations":       prepared.Citations,
		}
		if len(toolCalls) > 0 {
			meta["tool_calls"] = toolCalls
		}
		return sendEvent(w, flusher, "meta", meta)
	}
	result, streamErr := h.service.Stream(ctx, prepared, chat.Hooks{
		Delta: func(text string) error {
			if err := emitMeta(); err != nil {
				return err
			}
			return sendEvent(w, flusher, "delta", map[string]string{"text": text})
		},
		Tool: func(record chat.ToolRecord) { toolCalls = append(toolCalls, record) },
	})
	switch {
	case streamErr == nil, result.Partial && result.MessageID > 0:
		// 已产出内容并成功落库（哪怕生成中途失败）都以 done 收尾：这一轮对使用者是有效的。
		_ = emitMeta()
		_ = sendEvent(w, flusher, "done", map[string]any{"assistant_message_id": result.MessageID})
	default:
		// 一段都没产出、或落库失败：只能以 error 事件收尾。
		_ = sendEvent(w, flusher, "error", map[string]string{"error": "internal_error", "message": "生成失败，请稍后重试。"})
	}
}

// sendEvent 写一帧 SSE 并立即刷新，否则片段会滞留在服务端缓冲区。
func sendEvent(w http.ResponseWriter, flusher http.Flusher, name string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, encoded); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// writeAnswerError 把 prepare 阶段的错误映射为状态码：依赖不可用 503、会话不可见 404、入参 400。
func writeAnswerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound), errors.Is(err, agent.ErrNotFound):
		materialNotFound(w)
	case errors.Is(err, chat.ErrInvalidQuestion), errors.Is(err, chat.ErrNoAgentAvailable):
		apiError(w, 400, "invalid_request", "请求无效")
	default:
		// 含 rag.ErrDependency 与数据库故障：如实报服务不可用，不伪装成"资料里没有"。
		securityError(w, 503)
	}
}
