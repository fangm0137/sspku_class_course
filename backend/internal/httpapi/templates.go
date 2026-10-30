package httpapi

import (
	"errors"
	"log"
	"net/http"

	promptlib "campusclaw/backend/internal/prompt"
)

// templatesHandler 暴露提示词模板的读写。模板按班级隔离，学生只读。
type templatesHandler struct {
	store *promptlib.Store
}

type templateView struct {
	ID          uint64 `json:"id"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type templateWrite struct {
	Name        string `json:"name"`
	Body        string `json:"body"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

func templateViewOf(row promptlib.Row) templateView {
	return templateView{ID: row.ID, Name: row.Name, Body: row.Body, Description: row.Description, Enabled: row.Enabled}
}

// writeTemplateError 把存储层的哨兵错误翻译成状态码：
// 重名与字段不合规 400、仍被引用 409、跨班与不存在 404。
func writeTemplateError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, promptlib.ErrNameTaken):
		apiError(w, 400, "invalid_request", "班内已有同名模板")
	case errors.Is(err, promptlib.ErrInvalid):
		apiError(w, 400, "invalid_request", err.Error())
	case errors.Is(err, promptlib.ErrReferenced):
		apiError(w, 409, "conflict", "该模板仍被应用引用，先解除引用再删除")
	case errors.Is(err, promptlib.ErrNotFound):
		materialNotFound(w)
	default:
		log.Printf("prompt template operation failed: %v", err)
		securityError(w, 503)
	}
}

func (h templatesHandler) list(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	// 只有教师能通过 ?all=1 看到停用的模板；学生只看到启用的。
	includeDisabled := user.Role == "teacher" && r.URL.Query().Get("all") == "1"
	rows, err := h.store.List(r.Context(), user.ClassID, includeDisabled)
	if err != nil {
		securityError(w, 503)
		return
	}
	items := make([]templateView, 0, len(rows))
	for _, row := range rows {
		items = append(items, templateViewOf(row))
	}
	jsonReply(w, 200, map[string]any{"items": items, "placeholders": promptlib.Placeholders()})
}

func (h templatesHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	var input templateWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	id, err := h.store.Create(r.Context(), user.ClassID, input.Name, input.Body, input.Description, input.Enabled)
	if err != nil {
		writeTemplateError(w, err)
		return
	}
	jsonReply(w, 201, map[string]uint64{"id": id})
}

func (h templatesHandler) get(w http.ResponseWriter, r *http.Request) {
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
	row, err := h.store.Get(r.Context(), user.ClassID, id)
	if err != nil {
		writeTemplateError(w, err)
		return
	}
	jsonReply(w, 200, templateViewOf(row))
}

func (h templatesHandler) update(w http.ResponseWriter, r *http.Request) {
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
	var input templateWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	if err := h.store.Update(r.Context(), user.ClassID, id, input.Name, input.Body, input.Description, input.Enabled); err != nil {
		writeTemplateError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func (h templatesHandler) remove(w http.ResponseWriter, r *http.Request) {
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
		writeTemplateError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
