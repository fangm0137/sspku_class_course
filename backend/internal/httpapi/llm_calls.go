package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"campusclaw/backend/internal/audit"
)

// llmCallsHandler 是"究竟给模型发了什么"的查询入口。
// 范围口径：教师看本班，学生只看自己；越权与不存在一律 404。
type llmCallsHandler struct {
	store *audit.Store
}

// filterOf 把登录身份翻译成查询范围。范围来自令牌，不来自请求参数。
func (h llmCallsHandler) filterOf(r *http.Request) audit.Filter {
	user := identityOf(r)
	filter := audit.Filter{ClassID: user.ClassID, UserID: user.ID, Teacher: user.Role == "teacher"}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			filter.Limit = value
		}
	}
	return filter
}

func (h llmCallsHandler) list(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	rows, err := h.store.List(r.Context(), h.filterOf(r))
	if err != nil {
		securityError(w, 503)
		return
	}
	jsonReply(w, 200, map[string]any{"items": rows})
}

func (h llmCallsHandler) get(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	detail, err := h.store.Get(r.Context(), h.filterOf(r), id)
	if err != nil {
		writeAuditError(w, err)
		return
	}
	jsonReply(w, 200, detail)
}

// contextSize 回答第 3 问：材料正文总字数与本次实际注入字数。
func (h llmCallsHandler) contextSize(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	size, err := h.store.ContextSize(r.Context(), h.filterOf(r), id)
	if err != nil {
		writeAuditError(w, err)
		return
	}
	jsonReply(w, 200, size)
}

func writeAuditError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, audit.ErrNotFound):
		materialNotFound(w)
	default:
		securityError(w, 503)
	}
}
