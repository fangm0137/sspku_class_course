// Package audit 记录"这一次到底给模型发了什么、又拿回了什么"。
//
// 它是验收手段而不是附属品：五个验证问题都靠这张表回答，所以请求体按原样保存，
// 同时把系统消息、用户消息、注入字符数三个派生列一并落库，让核对不必解析 JSON。
// 写入是旁路：失败只记日志，绝不改变回答的状态码。
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// 调用结果。
const (
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// 工具调用结果的三态。
const (
	ToolOK     = "ok"
	ToolDenied = "denied"
	ToolError  = "error"
)

// 资料区块的边界标记。与 chat 组装用户消息时使用的标记一致，
// audit_test 里用 chat.BuildMessages 的真实输出锁定这个约定，防止两边写歪。
const (
	materialMarker   = "资料：\n"
	questionMarker   = "\n\n问题："
	toolRoleName     = "tool"
	systemRoleName   = "system"
	userRoleName     = "user"
	excerptRuneLimit = 500
)

// Message 是审计需要看到的消息形状：只有角色与正文。
type Message struct {
	Role    string
	Content string
}

// Call 是一次模型调用的全部事实。RequestJSON 必须是真正发出的字节。
type Call struct {
	ClassID          uint64
	ConversationID   uint64
	UserID           uint64
	AgentID          uint64
	RoundNo          int
	Model            string
	RequestJSON      []byte
	Messages         []Message
	SkillName        string
	ToolDefCount     int
	ToolCallCount    int
	FinishReason     string
	ResponseText     string
	ResponseJSON     string
	HTTPStatus       int
	OK               bool
	ErrorText        string
	PromptTokens     int
	CompletionTokens int
	LatencyMS        int
}

// ToolRow 是一次工具调用的审计行。
type ToolRow struct {
	ClassID        uint64
	ConversationID uint64
	UserID         uint64
	LLMCallID      uint64
	RoundNo        int
	ServerID       uint64
	ServerName     string
	ToolName       string
	ArgumentsJSON  string
	Status         string
	ResultExcerpt  string
	ErrorText      string
	LatencyMS      int
}

// Row 是审计列表里的一行（不含请求体全文，避免列表接口过重）。
// 字段名与 openapi.yaml 一致：全部小写下划线，前端直接消费。
type Row struct {
	ID               uint64    `json:"id"`
	ConversationID   uint64    `json:"conversation_id"`
	UserID           uint64    `json:"user_id"`
	AgentID          uint64    `json:"agent_id"`
	RoundNo          int       `json:"round_no"`
	Model            string    `json:"model"`
	SystemPrompt     string    `json:"system_prompt"`
	UserPrompt       string    `json:"user_prompt"`
	MessageCount     int       `json:"message_count"`
	ContextChars     int       `json:"context_chars"`
	SkillName        string    `json:"skill_name"`
	ToolDefCount     int       `json:"tool_def_count"`
	ToolCallCount    int       `json:"tool_call_count"`
	FinishReason     string    `json:"finish_reason"`
	Status           string    `json:"status"`
	HTTPStatus       int       `json:"http_status"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	LatencyMS        int       `json:"latency_ms"`
	ErrorText        string    `json:"error_text"`
	CreatedAt        time.Time `json:"created_at"`
}

// Detail 是单条记录，含完整请求体与它的工具调用。
type Detail struct {
	Row
	RequestJSON  string        `json:"request_json"`
	ResponseText string        `json:"response_text"`
	ResponseJSON string        `json:"response_json"`
	ToolCalls    []ToolCallRow `json:"tool_calls"`
}

// ToolCallRow 是详情里附带的工具调用。
type ToolCallRow struct {
	ID            uint64    `json:"id"`
	RoundNo       int       `json:"round_no"`
	ServerName    string    `json:"server_name"`
	ToolName      string    `json:"tool_name"`
	ArgumentsJSON string    `json:"arguments_json"`
	Status        string    `json:"status"`
	ResultExcerpt string    `json:"result_excerpt"`
	ErrorText     string    `json:"error_text"`
	LatencyMS     int       `json:"latency_ms"`
	CreatedAt     time.Time `json:"created_at"`
}

// ContextSize 是第 3 问的对比口径：材料正文总字数 vs 本次实际注入字数。
type ContextSize struct {
	MaterialCount  int      `json:"material_count"`
	SourceChars    int      `json:"source_chars"`
	ContextChars   int      `json:"context_chars"`
	MaterialTitles []string `json:"material_titles"`
}

// Filter 限定一次查询的范围：教师看本班，学生只看自己。
type Filter struct {
	ClassID uint64
	UserID  uint64
	Teacher bool
	Limit   int
}

// Store 是审计的存储层。
type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// RecordCall 写入一行调用审计并返回编号。派生列由实际发送的消息计算得出。
func (s *Store) RecordCall(ctx context.Context, call Call) (uint64, error) {
	systemPrompt, userPrompt, contextChars := summarize(call.Messages)
	round := call.RoundNo
	if round < 1 {
		round = 1
	}
	status := StatusFailed
	if call.OK {
		status = StatusOK
	}
	var promptTokens, completionTokens any
	if call.PromptTokens > 0 {
		promptTokens = call.PromptTokens
	}
	if call.CompletionTokens > 0 {
		completionTokens = call.CompletionTokens
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO llm_calls (class_id,conversation_id,user_id,agent_id,round_no,model,request_json,
		    system_prompt,user_prompt,message_count,context_chars,skill_name,tool_def_count,tool_call_count,
		    finish_reason,response_text,response_json,http_status,status,error_text,prompt_tokens,completion_tokens,latency_ms)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		call.ClassID, nullableID(call.ConversationID), call.UserID, nullableID(call.AgentID), round, call.Model,
		string(call.RequestJSON), systemPrompt, userPrompt, len(call.Messages), contextChars,
		nullableString(call.SkillName), call.ToolDefCount, call.ToolCallCount,
		nullableString(call.FinishReason), nullableString(call.ResponseText), nullableString(call.ResponseJSON),
		call.HTTPStatus, status, nullableString(truncateRunes(call.ErrorText, 255)),
		promptTokens, completionTokens, call.LatencyMS)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// RecordToolCall 写入一行工具调用审计。令牌与请求头一律不在此结构中，写不进来。
func (s *Store) RecordToolCall(ctx context.Context, row ToolRow) error {
	round := row.RoundNo
	if round < 1 {
		round = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tool_calls (class_id,conversation_id,user_id,llm_call_id,round_no,server_id,server_name,
		    tool_name,arguments_json,status,result_excerpt,error_text,latency_ms)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ClassID, nullableID(row.ConversationID), row.UserID, nullableID(row.LLMCallID), round,
		nullableID(row.ServerID), row.ServerName, row.ToolName, row.ArgumentsJSON, row.Status,
		nullableString(truncateRunes(row.ResultExcerpt, excerptRuneLimit)),
		nullableString(truncateRunes(row.ErrorText, 255)), row.LatencyMS)
	return err
}

// List 按范围返回审计列表（新的在前）。
func (s *Store) List(ctx context.Context, filter Filter) ([]Row, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id,IFNULL(conversation_id,0),user_id,IFNULL(agent_id,0),round_no,model,system_prompt,user_prompt,
	           message_count,context_chars,IFNULL(skill_name,''),tool_def_count,tool_call_count,IFNULL(finish_reason,''),
	           status,http_status,IFNULL(prompt_tokens,0),IFNULL(completion_tokens,0),latency_ms,IFNULL(error_text,''),created_at
	          FROM llm_calls WHERE class_id=?`
	args := []any{filter.ClassID}
	if !filter.Teacher {
		query += " AND user_id=?"
		args = append(args, filter.UserID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Row, 0, limit)
	for rows.Next() {
		var item Row
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.UserID, &item.AgentID, &item.RoundNo, &item.Model,
			&item.SystemPrompt, &item.UserPrompt, &item.MessageCount, &item.ContextChars, &item.SkillName,
			&item.ToolDefCount, &item.ToolCallCount, &item.FinishReason, &item.Status, &item.HTTPStatus,
			&item.PromptTokens, &item.CompletionTokens, &item.LatencyMS, &item.ErrorText, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ErrNotFound 覆盖"不存在"与"越权"，调用方统一返回 404。
var ErrNotFound = errors.New("audit record not found")

// Get 读取单条记录，含完整请求体与工具调用。范围口径与 List 一致。
func (s *Store) Get(ctx context.Context, filter Filter, id uint64) (Detail, error) {
	query := `SELECT id,IFNULL(conversation_id,0),user_id,IFNULL(agent_id,0),round_no,model,system_prompt,user_prompt,
	           message_count,context_chars,IFNULL(skill_name,''),tool_def_count,tool_call_count,IFNULL(finish_reason,''),
	           status,http_status,IFNULL(prompt_tokens,0),IFNULL(completion_tokens,0),latency_ms,IFNULL(error_text,''),
	           created_at,request_json,IFNULL(response_text,''),IFNULL(response_json,'')
	          FROM llm_calls WHERE id=? AND class_id=?`
	args := []any{id, filter.ClassID}
	if !filter.Teacher {
		query += " AND user_id=?"
		args = append(args, filter.UserID)
	}
	var item Detail
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&item.ID, &item.ConversationID, &item.UserID, &item.AgentID, &item.RoundNo, &item.Model,
		&item.SystemPrompt, &item.UserPrompt, &item.MessageCount, &item.ContextChars, &item.SkillName,
		&item.ToolDefCount, &item.ToolCallCount, &item.FinishReason, &item.Status, &item.HTTPStatus,
		&item.PromptTokens, &item.CompletionTokens, &item.LatencyMS, &item.ErrorText, &item.CreatedAt,
		&item.RequestJSON, &item.ResponseText, &item.ResponseJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	toolCalls, err := s.toolCallsOf(ctx, item.ID)
	if err != nil {
		return Detail{}, err
	}
	item.ToolCalls = toolCalls
	return item, nil
}

// ContextSize 对比"绑定材料的正文总字数"与"本次注入的字符数"。
// 两个数字都由数据库算：前者来自 materials 的正文，后者是审计里的派生列。
func (s *Store) ContextSize(ctx context.Context, filter Filter, id uint64) (ContextSize, error) {
	detail, err := s.Get(ctx, filter, id)
	if err != nil {
		return ContextSize{}, err
	}
	size := ContextSize{ContextChars: detail.ContextChars}
	if detail.AgentID == 0 {
		return size, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.original_name, CHAR_LENGTH(k.extracted_text)
		 FROM tutor_agent_materials b
		 JOIN materials m ON m.id=b.material_id
		 JOIN knowledge_entries k ON k.material_id=m.id
		 WHERE b.agent_id=? AND b.class_id=? ORDER BY m.id`, detail.AgentID, filter.ClassID)
	if err != nil {
		return ContextSize{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var title string
		var chars int
		if err := rows.Scan(&title, &chars); err != nil {
			return ContextSize{}, err
		}
		size.MaterialCount++
		size.SourceChars += chars
		size.MaterialTitles = append(size.MaterialTitles, title)
	}
	return size, rows.Err()
}

func (s *Store) toolCallsOf(ctx context.Context, callID uint64) ([]ToolCallRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,round_no,server_name,tool_name,arguments_json,status,IFNULL(result_excerpt,''),
		        IFNULL(error_text,''),latency_ms,created_at
		 FROM tool_calls WHERE llm_call_id=? ORDER BY id`, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ToolCallRow, 0, 2)
	for rows.Next() {
		var item ToolCallRow
		if err := rows.Scan(&item.ID, &item.RoundNo, &item.ServerName, &item.ToolName, &item.ArgumentsJSON,
			&item.Status, &item.ResultExcerpt, &item.ErrorText, &item.LatencyMS, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// summarize 从实际发送的消息里取出三个派生值：
// 系统消息全文、最后一条用户消息全文、注入字符数。
//
// 注入字符数只数"随检索与工具带进来的内容"：用户消息里「资料：」到「问题：」之间的部分，
// 加上全部 role=tool 的工具结果。历史对话不计入——它不属于"这次给模型的资料"。
func summarize(messages []Message) (string, string, int) {
	system := ""
	user := ""
	contextChars := 0
	for index, message := range messages {
		switch message.Role {
		case systemRoleName:
			if system == "" {
				system = message.Content
			}
		case userRoleName:
			if index == len(messages)-1 || user == "" {
				user = message.Content
			}
		case toolRoleName:
			contextChars += len([]rune(message.Content))
		}
	}
	if at := strings.Index(user, materialMarker); at >= 0 {
		rest := user[at+len(materialMarker):]
		if end := strings.LastIndex(rest, questionMarker); end >= 0 {
			contextChars += len([]rune(rest[:end]))
		}
	}
	return system, user, contextChars
}

// Derive 暴露派生规则，供测试与排障核对（三条派生列都从它来）。
func Derive(messages []Message) (string, string, int) { return summarize(messages) }

// SniffMessages 把请求体解析成消息列表。用于测试里核对"存的与发的一致"。
func SniffMessages(requestJSON []byte) ([]Message, error) {
	var payload struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(requestJSON, &payload); err != nil {
		return nil, err
	}
	return payload.Messages, nil
}

func nullableID(value uint64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
