package httpapi

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/mcp"
	"campusclaw/backend/internal/skill"
)

// 技能上传的限制：与 JSON 创建共用同一套解析与校验，区别只在"原文从哪来"。
const (
	skillUploadField = "file"
	// skillUploadLimit 是文件正文上限（64 KiB）。
	skillUploadLimit = 64 << 10
	// skillFormLimit 给 multipart 边界与请求头留余量。
	skillFormLimit = skillUploadLimit + (64 << 10)
)

// skillsHandler 暴露技能的读写。原文（source_text）是教师编辑技能的唯一输入接口，
// 表的每一列都能从文件解析得到，因此提交、修改与上传都只接受文件原文。
type skillsHandler struct {
	store *skill.Store
	// servers 用于保存时校验「工具名@服务器名」的四类条件；为空表示未接入 MCP。
	servers *mcp.Store
}

type skillView struct {
	ID          uint64   `json:"id"`
	Name        string   `json:"name"`
	TriggerText string   `json:"trigger_text"`
	Description string   `json:"description"`
	SubjectText string   `json:"subject_text"`
	Tools       []string `json:"tools"`
	Enabled     bool     `json:"enabled"`
	// Builtin 让界面能区分"平台自带"与"教师上传"。
	Builtin bool `json:"builtin"`
}

type skillWrite struct {
	SourceText string `json:"source_text"`
}

func skillViewOf(row skill.Row) skillView {
	tools := row.Tools
	if len(tools) == 0 {
		tools = []string{skill.KnowledgeSearchTool}
	}
	return skillView{
		ID: row.ID, Name: row.Name, TriggerText: row.TriggerText, Description: row.Description,
		SubjectText: row.SubjectText, Tools: tools, Enabled: row.Enabled, Builtin: row.Builtin,
	}
}

// writeSkillError 把解析错误原样交给教师（带行号与字段名），其余映射为状态码。
func writeSkillError(w http.ResponseWriter, err error) {
	var parseError *skill.ParseError
	var refError *mcp.RefError
	switch {
	case err == nil:
		return
	case errors.As(err, &parseError):
		apiError(w, 400, "invalid_request", parseError.Error())
	case errors.As(err, &refError):
		apiError(w, 400, "invalid_request", refError.Error())
	case errors.Is(err, skill.ErrNameTaken):
		apiError(w, 400, "invalid_request", "班内已有同名技能")
	case errors.Is(err, skill.ErrBuiltin):
		apiError(w, 409, "conflict", "平台自带技能不可删除，可以停用")
	case errors.Is(err, skill.ErrNotFound):
		materialNotFound(w)
	default:
		securityError(w, 503)
	}
}

// externalRefs 去掉内置工具，只留需要数据库校验的外部引用。
func externalRefs(tools []string) []string {
	refs := make([]string, 0, len(tools))
	for _, tool := range tools {
		if !skill.IsBuiltinTool(tool) {
			refs = append(refs, tool)
		}
	}
	return refs
}

// checkTools 是配置期校验：四类条件全查，任一不满足即拒绝写入。
func (h skillsHandler) checkTools(r *http.Request, classID uint64, tools []string) error {
	refs := externalRefs(tools)
	if len(refs) == 0 || h.servers == nil {
		return nil
	}
	return h.servers.ValidateRefs(r.Context(), classID, refs)
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
	h.createFromSource(w, r, user.ClassID, input.SourceText)
}

// createFromSource 是 JSON 创建与文件上传的共同落库路径：
// 两者都先 Parse、再查工具引用、最后写入，不存在"上传跳过某条校验"的分支。
func (h skillsHandler) createFromSource(w http.ResponseWriter, r *http.Request, classID uint64, source string) {
	definition, err := skill.Parse(source)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if err := h.checkTools(r, classID, definition.Tools); err != nil {
		writeSkillError(w, err)
		return
	}
	id, err := h.store.Create(r.Context(), classID, definition, nil)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	jsonReply(w, 201, map[string]uint64{"id": id})
}

// upload 接受 multipart/form-data 里的单个文件字段，正文按 UTF-8 文本处理。
func (h skillsHandler) upload(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		securityError(w, 503)
		return
	}
	user := identityOf(r)
	r.Body = http.MaxBytesReader(w, r.Body, skillFormLimit)
	if err := r.ParseMultipartForm(skillFormLimit); err != nil {
		apiError(w, 400, "invalid_request", "上传失败：请求不是 multipart/form-data 或文件过大")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	source, reason := readSkillFile(r)
	if reason != "" {
		apiError(w, 400, "invalid_request", reason)
		return
	}
	h.createFromSource(w, r, user.ClassID, source)
}

// readSkillFile 读文件并做三项检查：扩展名、大小、UTF-8。返回可读的拒绝原因。
func readSkillFile(r *http.Request) (string, string) {
	file, header, err := r.FormFile(skillUploadField)
	if err != nil {
		return "", "缺少文件字段 " + skillUploadField
	}
	defer file.Close()
	if reason := checkSkillFilename(header); reason != "" {
		return "", reason
	}
	content, err := io.ReadAll(io.LimitReader(file, skillUploadLimit+1))
	if err != nil {
		return "", "读取文件失败"
	}
	if len(content) > skillUploadLimit {
		return "", "文件超过 64 KiB，请精简后再上传"
	}
	if len(content) == 0 {
		return "", "文件为空"
	}
	if !utf8.Valid(content) {
		return "", "文件不是 UTF-8 文本"
	}
	return string(content), ""
}

func checkSkillFilename(header *multipart.FileHeader) string {
	extension := strings.ToLower(filepath.Ext(header.Filename))
	if extension != ".txt" && extension != ".md" {
		return "只接受 .txt 或 .md 技能文件"
	}
	return ""
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
	if err := h.checkTools(r, user.ClassID, definition.Tools); err != nil {
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
