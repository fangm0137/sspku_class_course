package httpapi

import (
	"errors"
	"net/http"

	"campusclaw/backend/internal/skill"
)

// skillsHandler 暴露技能的读写。原文（source_text）是教师编辑技能的唯一输入接口，
// 表的每一列都能从文件解析得到，因此提交与修改都只接受文件原文。
type skillsHandler struct {
	store *skill.Store
}

type skillView struct {
	ID          uint64   `json:"id"`
	Name        string   `json:"name"`
	TriggerText string   `json:"trigger_text"`
	Description string   `json:"description"`
	SubjectText string   `json:"subject_text"`
	Tools       []string `json:"tools"`
	Enabled     bool     `json:"enabled"`
}

type skillWrite struct {
	SourceText string `json:"source_text"`
}

func skillViewOf(row skill.Row) skillView {
	return skillView{
		ID: row.ID, Name: row.Name, TriggerText: row.TriggerText, Description: row.Description,
		SubjectText: row.SubjectText, Tools: []string{skill.KnowledgeSearchTool}, Enabled: row.Enabled,
	}
}

// writeSkillError 把解析错误原样交给教师（带行号与字段名），其余映射为状态码。
func writeSkillError(w http.ResponseWriter, err error) {
	var parseError *skill.ParseError
	switch {
	case err == nil:
		return
	case errors.As(err, &parseError):
		apiError(w, 400, "invalid_request", parseError.Error())
	case errors.Is(err, skill.ErrNameTaken):
		apiError(w, 400, "invalid_request", "班内已有同名技能")
	case errors.Is(err, skill.ErrNotFound):
		materialNotFound(w)
	default:
		securityError(w, 503)
	}
}

func (h skillsHandler) list(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	rows, err := h.store.List(r.Context(), user.ClassID)
	if err != nil {
		securityError(w, 503)
		return
	}
	items := make([]skillView, 0, len(rows))
	for _, row := range rows {
		items = append(items, skillViewOf(row))
	}
	jsonReply(w, 200, map[string]any{"items": items})
}

func (h skillsHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	var input skillWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	definition, err := skill.Parse(input.SourceText)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	id, err := h.store.Create(r.Context(), user.ClassID, definition, nil)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	jsonReply(w, 201, map[string]uint64{"id": id})
}

func (h skillsHandler) get(w http.ResponseWriter, r *http.Request) {
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
	detail, err := h.store.Detail(r.Context(), user.ClassID, id)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	jsonReply(w, 200, map[string]any{
		"skill":       skillViewOf(detail.Row),
		"source_text": detail.SourceText,
	})
}

func (h skillsHandler) update(w http.ResponseWriter, r *http.Request) {
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
	var input skillWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	definition, err := skill.Parse(input.SourceText)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if err := h.store.Update(r.Context(), user.ClassID, id, definition); err != nil {
		writeSkillError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func (h skillsHandler) remove(w http.ResponseWriter, r *http.Request) {
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
		writeSkillError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
