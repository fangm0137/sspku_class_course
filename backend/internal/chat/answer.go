package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/agent"
	"campusclaw/backend/internal/rag"
	"campusclaw/backend/internal/skill"
)

var (
	// ErrInvalidQuestion 表示提问为空、过短或过长，调用方返回 400。
	ErrInvalidQuestion = errors.New("invalid question")
	// ErrNoAgentAvailable 表示本班没有已启用的智能体，调用方返回 400。
	ErrNoAgentAvailable = errors.New("no enabled agent in this class")
)

// Service 编排一次回答：会话与智能体 → 技能路由 → 绑定材料内检索 → 消息组装 → 流式生成 → 落库。
type Service struct {
	chats     *Store
	agents    *agent.Store
	skills    *skill.Store
	retrieval *rag.Service
}

func New(db *sql.DB, retrieval *rag.Service) *Service {
	return &Service{chats: NewStore(db), agents: agent.New(db), skills: skill.New(db), retrieval: retrieval}
}

func (s *Service) Conversations() *Store { return s.chats }
func (s *Service) Agents() *agent.Store  { return s.agents }
func (s *Service) Skills() *skill.Store  { return s.skills }

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
}

// CountSkills 返回候选技能条数，便于测试与排障时观察候选集大小。
func (s *Service) CandidateSkills(ctx context.Context, classID, agentID uint64) ([]skill.Row, error) {
	bound, err := s.agents.BoundSkills(ctx, classID, agentID)
	if err != nil {
		return nil, err
	}
	return s.skills.ListByIDs(ctx, classID, bound)
}

// Prepare 完成所有可提前判定的工作：提问校验、会话归属、智能体、技能路由、检索与消息组装。
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
	messages, citations := BuildMessages(BuildInput{
		AgentPrompt: selected.Prompt,
		Skill:       picked,
		HasSkill:    hasSkill,
		Hits:        hits,
		Question:    question,
		History:     history,
	})
	return Prepared{
		Conversation:  conversation,
		Agent:         selected,
		Skill:         picked,
		HasSkill:      hasSkill,
		Hits:          hits,
		Citations:     citations,
		Messages:      messages,
		UserMessageID: userMessageID,
	}, nil
}

// Stream 执行生成：每段增量先经编号过滤器再交给 emit，最后把助手消息（连同引用与技能名）落库。
// partial 表示是否已有内容产出——调用方据此决定用 done 还是 error 收尾。
func (s *Service) Stream(ctx context.Context, prepared Prepared, emit func(string) error) (uint64, string, bool, error) {
	filter := FilterFor(len(prepared.Citations) > 0)
	var answer strings.Builder
	if s.retrieval == nil {
		return 0, "", false, rag.ErrDependency
	}
	_, err := s.retrieval.AnswerStream(ctx, prepared.Messages, rag.StreamOptions{}, func(delta string) error {
		text := filter.Push(delta)
		if text == "" {
			return nil
		}
		answer.WriteString(text)
		return emit(text)
	})
	if tail := filter.Flush(); tail != "" {
		answer.WriteString(tail)
		if emitErr := emit(tail); emitErr != nil && err == nil {
			err = emitErr
		}
	}
	partial := answer.Len() > 0
	if err != nil && !partial {
		// 一段都没产出：把失败如实上报给调用方（流已开始则由它以 error 事件收尾）。
		return 0, "", false, err
	}
	var citations json.RawMessage
	skillName := ""
	if len(prepared.Citations) > 0 {
		if encoded, marshalErr := json.Marshal(prepared.Citations); marshalErr == nil {
			citations = encoded
		}
	}
	if prepared.HasSkill {
		skillName = prepared.Skill.Name
	}
	id, persistErr := s.chats.FinishTurn(ctx, prepared.Conversation.ID, answer.String(), citations, skillName)
	if persistErr != nil {
		return 0, answer.String(), partial, persistErr
	}
	return id, answer.String(), partial, nil
}
