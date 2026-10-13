// Package chat 负责会话与消息的持久化、上下文窗口，以及一次回答的消息组装与流式编排。
// 会话是私有资源：读取与写入都由调用方传入已从登录用户派生的班级与用户编号。
package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// PlaceholderTitle 是新建会话时的占位标题。
	PlaceholderTitle = "新的提问"
	// TitleRunes 是首轮提问改写标题时保留的字数。
	TitleRunes = 24
	// ContextMessages 与 ContextMessageRunes 定义上下文窗口：末尾 6 条、每条 500 字。
	ContextMessages     = 6
	ContextMessageRunes = 500
	// MaterialRunes 是单条资料进提示词时的截断长度。
	MaterialRunes = 800
	// MinQuestionRunes 与 MaxQuestionRunes 限定提问长度。
	MinQuestionRunes = 2
	MaxQuestionRunes = 500
)

// ErrNotFound 覆盖"不存在""他人""他班"三种情形，调用方统一返回 404。
var ErrNotFound = errors.New("conversation not found")

// Conversation 是一轮连续追问的容器。
type Conversation struct {
	ID        uint64
	ClassID   uint64
	UserID    uint64
	AgentID   uint64
	Title     string
	UpdatedAt time.Time
}

// Message 是一条消息。Citations 为 nil 表示这条消息没有引用（用户消息一律为 nil）。
type Message struct {
	ID        uint64
	Role      string
	Content   string
	Citations json.RawMessage
	SkillName sql.NullString
	CreatedAt time.Time
}

// Store 是会话与消息的存储层。
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create 新建会话。agent_id 由调用方校验过后写入，此后不再变更。
func (s *Store) Create(ctx context.Context, classID, userID, agentID uint64) (uint64, error) {
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO conversations (class_id,user_id,agent_id,title) VALUES (?,?,?,?)",
		classID, userID, agentID, PlaceholderTitle)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// List 只返回本人的会话，按更新时间倒序。
func (s *Store) List(ctx context.Context, classID, userID uint64) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id,class_id,user_id,agent_id,title,updated_at FROM conversations WHERE user_id=? AND class_id=? ORDER BY updated_at DESC, id DESC",
		userID, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Conversation, 0, 8)
	for rows.Next() {
		var item Conversation
		if err := rows.Scan(&item.ID, &item.ClassID, &item.UserID, &item.AgentID, &item.Title, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Get 先按用户、再按班级过滤：任一不符都返回 ErrNotFound，与"不存在"不可区分。
func (s *Store) Get(ctx context.Context, classID, userID, id uint64) (Conversation, error) {
	var item Conversation
	err := s.db.QueryRowContext(ctx,
		"SELECT id,class_id,user_id,agent_id,title,updated_at FROM conversations WHERE id=? AND user_id=? AND class_id=?",
		id, userID, classID).
		Scan(&item.ID, &item.ClassID, &item.UserID, &item.AgentID, &item.Title, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, err
	}
	return item, nil
}

// Messages 按写入顺序返回该会话的全部消息。
func (s *Store) Messages(ctx context.Context, conversationID uint64) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id,role,content,citations_json,skill_name,created_at FROM messages WHERE conversation_id=? ORDER BY id", conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Message, 0, 8)
	for rows.Next() {
		var (
			item      Message
			citations []byte
		)
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &citations, &item.SkillName, &item.CreatedAt); err != nil {
			return nil, err
		}
		if len(citations) > 0 {
			item.Citations = json.RawMessage(citations)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Window 取上下文窗口：按写入顺序的末尾 limit 条，跳过非法角色与空正文，
// 每条正文截断到 perMessage 字。
func (s *Store) Window(ctx context.Context, conversationID uint64, limit, perMessage int) ([]Message, error) {
	if limit <= 0 {
		limit = ContextMessages
	}
	if perMessage <= 0 {
		perMessage = ContextMessageRunes
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,role,content FROM messages
		 WHERE conversation_id=? AND role IN ('user','assistant') AND content<>''
		 ORDER BY id DESC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reversed := make([]Message, 0, limit)
	for rows.Next() {
		var item Message
		if err := rows.Scan(&item.ID, &item.Role, &item.Content); err != nil {
			return nil, err
		}
		item.Content = truncateRunes(item.Content, perMessage)
		reversed = append(reversed, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 查询用 DESC 取末尾若干条，这里翻回写入顺序。
	items := make([]Message, len(reversed))
	for index, item := range reversed {
		items[len(reversed)-1-index] = item
	}
	return items, nil
}

// BeginTurn 先落库本轮提问（因此它必定落在上下文窗口内），并在标题仍是占位串时改写标题。
func (s *Store) BeginTurn(ctx context.Context, conversationID uint64, question string) (uint64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"UPDATE conversations SET title=CASE WHEN title=? THEN ? ELSE title END, updated_at=CURRENT_TIMESTAMP(6) WHERE id=?",
		PlaceholderTitle, TitleFor(question), conversationID); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx,
		"INSERT INTO messages (conversation_id,role,content) VALUES (?,'user',?)", conversationID, question)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// FinishTurn 写入助手消息：正文连同引用列表与本次命中的技能名一起持久化。
func (s *Store) FinishTurn(ctx context.Context, conversationID uint64, answer string, citations json.RawMessage, skillName string) (uint64, error) {
	var citationsValue any
	if len(citations) > 0 {
		citationsValue = string(citations)
	}
	var skillValue any
	if skillName != "" {
		skillValue = skillName
	}
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO messages (conversation_id,role,content,citations_json,skill_name) VALUES (?,'assistant',?,?,?)",
		conversationID, answer, citationsValue, skillValue)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// TitleFor 取提问的前 TitleRunes 字作为标题；截取结果为空时保留占位串。
// 这里自带 trim：标题是给列表看的，前后空白不该留进去。
func TitleFor(question string) string {
	title := truncateRunes(strings.TrimSpace(question), TitleRunes)
	if title == "" {
		return PlaceholderTitle
	}
	return title
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}
