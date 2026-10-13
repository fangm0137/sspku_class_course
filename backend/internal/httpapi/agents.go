package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"campusclaw/backend/internal/agent"
)

// agentsHandler 暴露智能体的读写与两份绑定。班级一律取自会话，请求里的班级字段不存在。
type agentsHandler struct {
	store *agent.Store
}

type agentView struct {
	ID          uint64   `json:"id"`
	Name        string   `json:"name"`
	Prompt      string   `json:"prompt"`
	Enabled     bool     `json:"enabled"`
	SkillIDs    []uint64 `json:"skill_ids"`
	MaterialIDs []uint64 `json:"material_ids"`
}

type agentWrite struct {
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Enabled bool   `json:"enabled"`
}

type idList struct {
	IDs []uint64 `json:"skill_ids"`
}

type materialIDList struct {
	IDs []uint64 `json:"material_ids"`
}

func viewOf(row agent.Row) agentView {
	return agentView{ID: row.ID, Name: row.Name, Prompt: row.Prompt, Enabled: row.Enabled, SkillIDs: row.SkillIDs, MaterialIDs: row.MaterialIDs}
}

// writeAgentError 把存储层的哨兵错误翻译成状态码：
// 重复名称 400、长度不合规 400、跨班与不存在 404、其余 503。
func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, agent.ErrNameTaken):
		apiError(w, 400, "invalid_request", "班内已有同名智能体")
	case errors.Is(err, agent.ErrInvalid):
		apiError(w, 400, "invalid_request", "名称或提示词长度不合规")
	case errors.Is(err, agent.ErrNotFound):
		materialNotFound(w)
	default:
		securityError(w, 503)
	}
}

// idFromPath 解析 {id}：必须是规范的正整数，否则按不存在处理。
func idFromPath(r *http.Request) (uint64, bool) {
	value := r.PathValue("id")
	id, err := strconv.ParseUint(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}

func (h agentsHandler) list(w http.ResponseWriter, r *http.Request) {
	user := identityOf(r)
	// 只有教师能通过 ?all=1 看到停用的智能体；学生视角一律只列已启用。
	includeDisabled := user.Role == "teacher" && r.URL.Query().Get("all") == "1"
	rows, err := h.store.List(r.Context(), user.ClassID, includeDisabled)
	if err != nil {
		securityError(w, 503)
		return
	}
	items := make([]agentView, 0, len(rows))
	for _, row := range rows {
		items = append(items, viewOf(row))
	}
	jsonReply(w, 200, map[string]any{"items": items})
}

func (h agentsHandler) create(w http.ResponseWriter, r *http.Request) {
	user := identityOf(r)
	var input agentWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	id, err := h.store.Create(r.Context(), user.ClassID, input.Name, input.Prompt, input.Enabled)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	jsonReply(w, 201, map[string]uint64{"id": id})
}

func (h agentsHandler) get(w http.ResponseWriter, r *http.Request) {
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	row, err := h.store.Get(r.Context(), user.ClassID, id)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	jsonReply(w, 200, viewOf(row))
}

func (h agentsHandler) update(w http.ResponseWriter, r *http.Request) {
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	var input agentWrite
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	if err := h.store.Update(r.Context(), user.ClassID, id, input.Name, input.Prompt, input.Enabled); err != nil {
		writeAgentError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func (h agentsHandler) remove(w http.ResponseWriter, r *http.Request) {
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	if err := h.store.Delete(r.Context(), user.ClassID, id); err != nil {
		writeAgentError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func (h agentsHandler) replaceSkills(w http.ResponseWriter, r *http.Request) {
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	var input idList
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	if err := h.store.ReplaceSkills(r.Context(), user.ClassID, id, input.IDs); err != nil {
		writeAgentError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

func (h agentsHandler) replaceMaterials(w http.ResponseWriter, r *http.Request) {
	user := identityOf(r)
	id, valid := idFromPath(r)
	if !valid {
		materialNotFound(w)
		return
	}
	var input materialIDList
	if !decodeJSON(r, jsonBodyLimit, &input) {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	if err := h.store.ReplaceMaterials(r.Context(), user.ClassID, id, input.IDs); err != nil {
		writeAgentError(w, err)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
