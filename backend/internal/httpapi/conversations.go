package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"campusclaw/backend/internal/agent"
	"campusclaw/backend/internal/chat"
)

// conversationsHandler 暴露会话与消息的读取、新建与一次流式提问。
type conversationsHandler struct {
	chats  *chat.Store
	agents *agent.Store
}

type conversationView struct {
	ID        uint64 `json:"id"`
	Title     string `json:"title"`
	AgentID   uint64 `json:"agent_id"`
	UpdatedAt string `json:"updated_at"`
}

type messageView struct {
	ID        uint64          `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Citations json.RawMessage `json:"citations,omitempty"`
	SkillName string          `json:"skill_name,omitempty"`
	CreatedAt string          `json:"created_at"`
}

type conversationWrite struct {
	AgentID uint64 `json:"agent_id"`
}

func conversationViewOf(item chat.Conversation) conversationView {
	return conversationView{ID: item.ID, Title: item.Title, AgentID: item.AgentID, UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339)}
}

func (h conversationsHandler) list(w http.ResponseWriter, r *http.Request) {
	if h.chats == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	rows, err := h.chats.List(r.Context(), user.ClassID, user.ID)
	if err != nil {
		securityError(w, 503)
		return
	}
	items := make([]conversationView, 0, len(rows))
	for _, row := range rows {
		items = append(items, conversationViewOf(row))
	}
	jsonReply(w, 200, map[string]any{"items": items})
}

// create 先确认本班有可用的智能体（否则 400），再校验所选智能体属于本班且已启用（否则 404）。
func (h conversationsHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.chats == nil || h.agents == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	var input conversationWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	available, err := h.agents.List(r.Context(), user.ClassID, false)
	if err != nil {
		securityError(w, 503)
		return
	}
	if len(available) == 0 {
		apiError(w, 400, "invalid_request", "本班暂无可用的智能体")
		return
	}
	selected, err := h.agents.Get(r.Context(), user.ClassID, input.AgentID)
	if err != nil || !selected.Enabled {
		// 不存在、他班与未启用一律按不存在处理，响应完全一致。
		materialNotFound(w)
		return
	}
	id, err := h.chats.Create(r.Context(), user.ClassID, user.ID, input.AgentID)
	if err != nil {
		securityError(w, 503)
		return
	}
	jsonReply(w, 201, map[string]any{"id": id, "title": chat.PlaceholderTitle})
}

func (h conversationsHandler) get(w http.ResponseWriter, r *http.Request) {
	if h.chats == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	conversation, err := h.chats.Get(r.Context(), user.ClassID, user.ID, id)
	if err != nil {
		writeChatError(w, err)
		return
	}
	rows, err := h.chats.Messages(r.Context(), id)
	if err != nil {
		securityError(w, 503)
		return
	}
	messages := make([]messageView, 0, len(rows))
	for _, row := range rows {
		item := messageView{
			ID: row.ID, Role: row.Role, Content: row.Content,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		}
		if len(row.Citations) > 0 {
			item.Citations = row.Citations
		}
		if row.SkillName.Valid {
			item.SkillName = row.SkillName.String
		}
		messages = append(messages, item)
	}
	view := conversationViewOf(conversation)
	jsonReply(w, 200, map[string]any{
		"id": view.ID, "title": view.Title, "agent_id": view.AgentID,
		"updated_at": view.UpdatedAt, "messages": messages,
	})
}

// writeChatError 把会话层的哨兵错误映射为状态码：会话不可见与不存在同为 404。
func writeChatError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, chat.ErrNotFound), errors.Is(err, agent.ErrNotFound):
		materialNotFound(w)
	case errors.Is(err, chat.ErrInvalidQuestion), errors.Is(err, chat.ErrNoAgentAvailable):
		apiError(w, 400, "invalid_request", "请求无效")
	default:
		securityError(w, 503)
	}
}
