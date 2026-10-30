package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/agent"
	"campusclaw/backend/internal/audit"
	"campusclaw/backend/internal/mcp"
	promptlib "campusclaw/backend/internal/prompt"
	"campusclaw/backend/internal/rag"
	"campusclaw/backend/internal/skill"
)

var (
	// ErrInvalidQuestion 表示提问为空、过短或过长，调用方返回 400。
	ErrInvalidQuestion = errors.New("invalid question")
	// ErrNoAgentAvailable 表示本班没有已启用的智能体，调用方返回 400。
	ErrNoAgentAvailable = errors.New("no enabled agent in this class")
)

// Options 是编排层的可选配置。缺省即第 5 课行为：不接外部工具、不写审计。
type Options struct {
	Settings mcp.Settings
}

// Service 编排一次回答：会话与智能体 → 技能路由 → 绑定材料内检索 → 消息组装 → 工具循环 → 落库。
type Service struct {
	chats     *Store
	agents    *agent.Store
	skills    *skill.Store
	templates *promptlib.Store
	servers   *mcp.Store
	audit     *audit.Store
	retrieval *rag.Service
	settings  mcp.Settings
}

func New(db *sql.DB, retrieval *rag.Service, options ...Options) *Service {
	settings := mcp.Settings{
		CallTimeout: mcp.DefaultCallTimeout, MaxRounds: mcp.DefaultMaxRounds, MaxCalls: mcp.DefaultMaxCalls,
	}
	if len(options) > 0 && options[0].Settings.CallTimeout > 0 {
		settings = options[0].Settings
	}
	service := &Service{
		chats: NewStore(db), agents: agent.New(db), skills: skill.New(db),
		templates: promptlib.NewStore(db), audit: audit.New(db),
		retrieval: retrieval, settings: settings,
	}
	// 令牌密钥不可用时只关闭 MCP，不阻断启动：登记过的服务器会一直显示"令牌无法解密"。
	if servers, err := mcp.NewStore(db, settings); err == nil {
		service.servers = servers
	}
	return service
}

func (s *Service) Conversations() *Store       { return s.chats }
func (s *Service) Agents() *agent.Store        { return s.agents }
func (s *Service) Skills() *skill.Store        { return s.skills }
func (s *Service) Templates() *promptlib.Store { return s.templates }
func (s *Service) Servers() *mcp.Store         { return s.servers }
func (s *Service) Audit() *audit.Store         { return s.audit }
func (s *Service) Settings() mcp.Settings      { return s.settings }

// Prepared 是一次回答在推送第一帧之前就已经确定的全部内容。
type Prepared struct {
	Conversation  Conversation
	Agent         agent.Row
	Skill         skill.Row
	HasSkill      bool
	Hits          []rag.Hit
	Citations     []Citation
	Messages      []rag.ChatMessage
	UserMessageID uint64
	ClassID       uint64
	UserID        uint64
	// Declarations 是本轮候选工具（来自命中技能声明的引用），为空表示不带 tools。
	Declarations []mcp.Declaration
}

// CandidateSkills 返回候选技能，便于测试与排障时观察候选集大小。
func (s *Service) CandidateSkills(ctx context.Context, classID, agentID uint64) ([]skill.Row, error) {
	bound, err := s.agents.BoundSkills(ctx, classID, agentID)
	if err != nil {
		return nil, err
	}
	return s.skills.ListByIDs(ctx, classID, bound)
}

// Prepare 完成所有可提前判定的工作：提问校验、会话归属、智能体、提示词、技能路由、
// 检索、候选工具与消息组装。
// 出错语义：ErrNotFound → 404；ErrInvalidQuestion / ErrNoAgentAvailable → 400；
// rag.ErrDependency → 503。这些失败都发生在写第一帧之前，因此仍能返回状态码。
func (s *Service) Prepare(ctx context.Context, classID, userID, conversationID uint64, question string) (Prepared, error) {
	question = strings.TrimSpace(question)
	if length := utf8.RuneCountInString(question); length < MinQuestionRunes || length > MaxQuestionRunes {
		return Prepared{}, ErrInvalidQuestion
	}
	conversation, err := s.chats.Get(ctx, classID, userID, conversationID)
	if err != nil {
		return Prepared{}, err
	}
	// 会话锁定的智能体：即使它已被停用，已有会话也照常提问（停用只拦住新建时的选用）。
	selected, err := s.agents.Get(ctx, classID, conversation.AgentID)
	if err != nil {
		return Prepared{}, err
	}
	// 先把本轮提问落库，标题在仍是占位串时改写；随后取窗口，本轮提问必定在其中。
	userMessageID, err := s.chats.BeginTurn(ctx, conversationID, question)
	if err != nil {
		return Prepared{}, err
	}
	window, err := s.chats.Window(ctx, conversationID, ContextMessages, ContextMessageRunes)
	if err != nil {
		return Prepared{}, err
	}
	history := window
	if len(window) > 0 && window[len(window)-1].ID == userMessageID {
		// 本轮提问单独放进用户消息，历史里不再重复它。
		history = window[:len(window)-1]
	}

	candidates, err := s.CandidateSkills(ctx, classID, selected.ID)
	if err != nil {
		return Prepared{}, err
	}
	picked, hasSkill := skill.Route(question, candidates)

	subject := ""
	if hasSkill {
		subject = picked.SubjectText
	}
	scope, err := s.agents.ScopedMaterials(ctx, classID, selected.ID, subject)
	if err != nil {
		return Prepared{}, err
	}
	var hits []rag.Hit
	if len(scope) > 0 && s.retrieval != nil {
		hits, err = s.retrieval.Search(ctx, classID, question, rag.SearchOptions{
			Mode: rag.ModeHybrid, Limit: rag.TopK, MaterialIDs: scope,
		})
		if err != nil {
			return Prepared{}, err
		}
	}
	// 系统提示词的来源优先级：引用模板 → 自由文本 → 默认角色。
	basePrompt := s.resolvePrompt(ctx, classID, selected, question)
	messages, citations := BuildMessages(BuildInput{
		AgentPrompt: basePrompt,
		Skill:       picked,
		HasSkill:    hasSkill,
		Hits:        hits,
		Question:    question,
		History:     history,
	})
	declarations, err := s.declarationsOf(ctx, classID, picked, hasSkill)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{
		Conversation:  conversation,
		Agent:         selected,
		Skill:         picked,
		HasSkill:      hasSkill,
		Hits:          hits,
		Citations:     citations,
		Messages:      messages,
		UserMessageID: userMessageID,
		ClassID:       classID,
		UserID:        userID,
		Declarations:  declarations,
	}, nil
}

// resolvePrompt 渲染模板或退回自由文本。模板被删或读不到时退回自由文本而不是报错：
// 一个应用不该因为模板行不见了就无法回答。
func (s *Service) resolvePrompt(ctx context.Context, classID uint64, selected agent.Row, question string) string {
	if selected.PromptTemplateID == nil || s.templates == nil {
		return selected.Prompt
	}
	template, err := s.templates.Get(ctx, classID, *selected.PromptTemplateID)
	if err != nil {
		return selected.Prompt
	}
	// 渲染一次、不递归：提问里出现 {{today}} 会原样留在输出中。
	return promptlib.Render(template.Body, selected.Name, question)
}

// declarationsOf 把命中技能声明的工具引用解析成"当前可用"的声明。
// 运行期只判可用性，不重做配置期的四类校验。
func (s *Service) declarationsOf(ctx context.Context, classID uint64, picked skill.Row, hasSkill bool) ([]mcp.Declaration, error) {
	if !hasSkill || s.servers == nil {
		return nil, nil
	}
	refs := make([]mcp.Ref, 0, len(picked.Tools))
	for _, tool := range picked.Tools {
		if skill.IsBuiltinTool(tool) {
			continue
		}
		ref, err := mcp.ParseRef(tool)
		if err != nil {
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return nil, nil
	}
	return s.servers.Available(ctx, classID, refs)
}

// toolDeclarations 把声明转成网关层的工具声明，parameters 原样取自发现结果。
func toolDeclarations(declarations []mcp.Declaration) []rag.ToolDecl {
	tools := make([]rag.ToolDecl, 0, len(declarations))
	for _, declaration := range declarations {
		tools = append(tools, rag.ToolDecl{
			Name: declaration.Name, Description: declaration.Description, Parameters: declaration.Parameters,
		})
	}
	return tools
}

// toolCallPayload 是 meta.tool_calls 里的一项，也是前端工具栏的全部依据。
type toolCallPayload struct {
	Ref       string `json:"ref"`
	Status    string `json:"status"`
	LatencyMS int    `json:"latency_ms"`
	Text      string `json:"text"`
}

// encodeCitations 把引用列表编码成落库用的 JSON；空列表返回 nil。
func encodeCitations(citations []Citation) json.RawMessage {
	if len(citations) == 0 {
		return nil
	}
	encoded, err := json.Marshal(citations)
	if err != nil {
		return nil
	}
	return encoded
}
