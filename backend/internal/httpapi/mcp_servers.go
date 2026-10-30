package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"campusclaw/backend/internal/mcp"
)

// mcpHandler 暴露 MCP 服务器的登记、连接与工具清单。写操作只有教师能做。
// 令牌只进不出：任何响应里都只有 has_token。
type mcpHandler struct {
	store *mcp.Store
}

type mcpServerView struct {
	ID          uint64 `json:"id"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	HasToken    bool   `json:"has_token"`
	Status      string `json:"status"`
	LastError   string `json:"last_error"`
	ConnectedAt string `json:"connected_at,omitempty"`
	ToolCount   int    `json:"tool_count"`
}

type mcpServerWrite struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	// Token 在登记时必填；修改时留空表示沿用原令牌。
	Token string `json:"token"`
}

type mcpToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// InputSchema 原样输出发现结果里的 JSON，不转义成字符串。
	InputSchema json.RawMessage `json:"input_schema"`
}

func mcpViewOf(server mcp.Server) mcpServerView {
	view := mcpServerView{
		ID: server.ID, Name: server.Name, BaseURL: server.BaseURL, HasToken: server.HasToken,
		Status: server.Status, LastError: server.LastError, ToolCount: server.ToolCount,
	}
	if server.ConnectedAt != nil {
		view.ConnectedAt = server.ConnectedAt.UTC().Format(time.RFC3339)
	}
	return view
}

func writeMCPError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, mcp.ErrNameTaken):
		apiError(w, 400, "invalid_request", "班内已有同名服务器")
	case errors.Is(err, mcp.ErrInvalid), errors.Is(err, mcp.ErrInvalidURL):
		apiError(w, 400, "invalid_request", err.Error())
	case errors.Is(err, mcp.ErrSecret):
		apiError(w, 500, "internal_error", "令牌无法解密，请重新填写令牌")
	case errors.Is(err, mcp.ErrNotFound):
		materialNotFound(w)
	default:
		securityError(w, 503)
	}
}

func (h mcpHandler) list(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	servers, err := h.store.List(r.Context(), user.ClassID)
	if err != nil {
		securityError(w, 503)
		return
	}
	items := make([]mcpServerView, 0, len(servers))
	for _, server := range servers {
		items = append(items, mcpViewOf(server))
	}
	jsonReply(w, 200, map[string]any{"items": items})
}

func (h mcpHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	var input mcpServerWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	id, err := h.store.Create(r.Context(), user.ClassID, input.Name, input.BaseURL, input.Token)
	if err != nil {
		writeMCPError(w, err)
		return
	}
	jsonReply(w, 201, map[string]uint64{"id": id})
}

func (h mcpHandler) update(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	var input mcpServerWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	if err := h.store.Update(r.Context(), user.ClassID, id, input.Name, input.BaseURL, input.Token); err != nil {
		writeMCPError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func (h mcpHandler) remove(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	if err := h.store.Delete(r.Context(), user.ClassID, id); err != nil {
		writeMCPError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

// connect 是"握手 + 发现 + 整体替换"。失败返回 502，并把可读原因写进 last_error；
// 问答主路径不依赖这一步，因此失败不影响已有对话。
func (h mcpHandler) connect(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	tools, err := h.store.Connect(r.Context(), user.ClassID, id)
	if err != nil {
		if errors.Is(err, mcp.ErrNotFound) {
			materialNotFound(w)
			return
		}
		reason := "连接失败"
		if server, getErr := h.store.Get(r.Context(), user.ClassID, id); getErr == nil && server.LastError != "" {
			reason = "连接失败：" + server.LastError
		}
		apiError(w, 502, "upstream_error", reason)
		return
	}
	items := make([]mcpToolView, 0, len(tools))
	for _, tool := range tools {
		items = append(items, mcpToolView{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	jsonReply(w, 200, map[string]any{"tools": items})
}

func (h mcpHandler) disconnect(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	if err := h.store.Disconnect(r.Context(), user.ClassID, id); err != nil {
		writeMCPError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func (h mcpHandler) tools(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	tools, err := h.store.Tools(r.Context(), user.ClassID, id)
	if err != nil {
		writeMCPError(w, err)
		return
	}
	items := make([]mcpToolView, 0, len(tools))
	for _, tool := range tools {
		items = append(items, mcpToolView{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	jsonReply(w, 200, map[string]any{"items": items})
}
