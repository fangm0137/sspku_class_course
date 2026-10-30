// Package agent 负责智能体及其绑定的读写与校验。
// 班级与资源归属一律由调用方从登录用户派生，本包不接受来自请求的班级参数。
package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

// 长度上限与数据库 CHECK 保持一致。
const (
	MaxNameRunes   = 64
	MaxPromptRunes = 2000
)

var (
	// ErrNotFound 覆盖"不存在"与"不属于本班"两种情形，调用方统一返回 404。
	ErrNotFound = errors.New("agent not found")
	// ErrNameTaken 表示班内名称重复。
	ErrNameTaken = errors.New("agent name already used in this class")
	// ErrInvalid 表示字段不满足长度或必填约束。
	ErrInvalid = errors.New("invalid agent field")
	// ErrBuiltin 表示这是平台自带应用：可停用、可改绑定，不可删除（409）。
	ErrBuiltin = errors.New("builtin agent cannot be deleted")
)

// Row 是一个智能体及其绑定。
type Row struct {
	ID      uint64
	ClassID uint64
	Name    string
	Prompt  string
	// PromptTemplateID 非空表示系统提示词来自模板；为空回落到 Prompt，再回落到默认角色。
	PromptTemplateID *uint64
	Enabled          bool
	// Builtin 为真表示平台自带（builtin_key 非空）。
	Builtin     bool
	SkillIDs    []uint64
	MaterialIDs []uint64
}

// NamedItem 是组成视图里的一项（技能或材料）。
type NamedItem struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// Composition 是"这个应用当前实际生效了什么"的只读视图。
type Composition struct {
	AgentID      uint64      `json:"agent_id"`
	Name         string      `json:"name"`
	Builtin      bool        `json:"builtin"`
	Prompt       string      `json:"prompt"`
	TemplateID   *uint64     `json:"template_id"`
	TemplateName string      `json:"template_name"`
	Skills       []NamedItem `json:"skills"`
	Materials    []NamedItem `json:"materials"`
	ToolRefs     []string    `json:"tool_refs"`
}

// Store 是智能体的存储层。
type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

const selectColumns = "id,class_id,name,prompt,prompt_template_id,enabled,builtin_key IS NOT NULL"

// List 返回本班智能体。includeDisabled 为假时只返回已启用的（学生视角）。
func (s *Store) List(ctx context.Context, classID uint64, includeDisabled bool) ([]Row, error) {
	query := "SELECT " + selectColumns + " FROM tutor_agents WHERE class_id=?"
	args := []any{classID}
	if !includeDisabled {
		query += " AND enabled=1"
	}
	query += " ORDER BY id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Row, 0, 4)
	for rows.Next() {
		var item Row
		var templateID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.ClassID, &item.Name, &item.Prompt, &templateID, &item.Enabled, &item.Builtin); err != nil {
			return nil, err
		}
		if templateID.Valid {
			value := uint64(templateID.Int64)
			item.PromptTemplateID = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachBindings(ctx, classID, items); err != nil {
		return nil, err
	}
	return items, nil
}

// Get 读取本班单个智能体，跨班与不存在都返回 ErrNotFound。
func (s *Store) Get(ctx context.Context, classID, id uint64) (Row, error) {
	var item Row
	var templateID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT "+selectColumns+" FROM tutor_agents WHERE id=? AND class_id=?", id, classID).
		Scan(&item.ID, &item.ClassID, &item.Name, &item.Prompt, &templateID, &item.Enabled, &item.Builtin)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, err
	}
	if templateID.Valid {
		value := uint64(templateID.Int64)
		item.PromptTemplateID = &value
	}
	items := []Row{item}
	if err := s.attachBindings(ctx, classID, items); err != nil {
		return Row{}, err
	}
	return items[0], nil
}

// Create 新建智能体。名称班内唯一由数据库唯一键兜底，冲突返回 ErrNameTaken。
func (s *Store) Create(ctx context.Context, classID uint64, name, prompt string, templateID *uint64, enabled bool) (uint64, error) {
	name = strings.TrimSpace(name)
	if err := validate(name, prompt); err != nil {
		return 0, err
	}
	if err := s.checkTemplate(ctx, classID, templateID); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO tutor_agents (class_id,name,prompt,prompt_template_id,enabled) VALUES (?,?,?,?,?)",
		classID, name, prompt, templateID, enabled)
	if err != nil {
		if isDuplicate(err) {
			return 0, ErrNameTaken
		}
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// Update 改写名称、提示词、模板引用与启用状态。整体覆盖，调用方需先读后写。
func (s *Store) Update(ctx context.Context, classID, id uint64, name, prompt string, templateID *uint64, enabled bool) error {
	name = strings.TrimSpace(name)
	if err := validate(name, prompt); err != nil {
		return err
	}
	if err := s.checkTemplate(ctx, classID, templateID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE tutor_agents SET name=?,prompt=?,prompt_template_id=?,enabled=? WHERE id=? AND class_id=?",
		name, prompt, templateID, enabled, id, classID)
	if err != nil {
		if isDuplicate(err) {
			return ErrNameTaken
		}
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// 内容未变时 RowsAffected 为 0，因此还要确认这一行是否真的存在。
		var exists int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tutor_agents WHERE id=? AND class_id=?", id, classID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// checkTemplate 校验模板引用：必须属于本班，且新引用必须是启用状态。
// 停用只拦住"新建引用"，已引用的应用照常渲染——口径与"应用停用后已有会话仍可提问"一致。
func (s *Store) checkTemplate(ctx context.Context, classID uint64, templateID *uint64) error {
	if templateID == nil {
		return nil
	}
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		"SELECT enabled FROM prompt_templates WHERE id=? AND class_id=?", *templateID, classID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("%w: 该模板已停用，不能设为应用当前引用", ErrInvalid)
	}
	return nil
}

// Delete 删除智能体。绑定表随外键级联清理；已存在的会话会因外键而无法保留，故一并删除。
// 平台自带应用不可删除：删掉就失去了"内置应用"这一能力的证据。
func (s *Store) Delete(ctx context.Context, classID, id uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	var builtin bool
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*), IFNULL(MAX(builtin_key IS NOT NULL),0) FROM tutor_agents WHERE id=? AND class_id=?", id, classID).
		Scan(&exists, &builtin); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	if builtin {
		return ErrBuiltin
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE agent_id=?)", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM conversations WHERE agent_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM tutor_agents WHERE id=? AND class_id=?", id, classID); err != nil {
		return err
	}
	return tx.Commit()
}

// Composition 组装只读视图：模板名、候选技能名、绑定材料标题与工具引用。
// 工具引用取自绑定技能的「工具」声明，去掉内置的 knowledge_search——
// 视图要说的是"这次能调到哪些外部工具"。
func (s *Store) Composition(ctx context.Context, classID, id uint64) (Composition, error) {
	row, err := s.Get(ctx, classID, id)
	if err != nil {
		return Composition{}, err
	}
	view := Composition{
		AgentID: row.ID, Name: row.Name, Builtin: row.Builtin, Prompt: row.Prompt,
		TemplateID: row.PromptTemplateID, Skills: []NamedItem{}, Materials: []NamedItem{}, ToolRefs: []string{},
	}
	if row.PromptTemplateID != nil {
		_ = s.db.QueryRowContext(ctx,
			"SELECT name FROM prompt_templates WHERE id=? AND class_id=?", *row.PromptTemplateID, classID).
			Scan(&view.TemplateName)
	}
	if err := s.fillSkills(ctx, classID, row.ID, &view); err != nil {
		return Composition{}, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.original_name FROM tutor_agent_materials b JOIN materials m ON m.id=b.material_id
		 WHERE b.agent_id=? AND b.class_id=? ORDER BY m.id`, row.ID, classID)
	if err != nil {
		return Composition{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var materialID uint64
		var name string
		if err := rows.Scan(&materialID, &name); err != nil {
			return Composition{}, err
		}
		view.Materials = append(view.Materials, NamedItem{ID: materialID, Name: displayTitle(name)})
	}
	return view, rows.Err()
}

func (s *Store) fillSkills(ctx context.Context, classID, agentID uint64, view *Composition) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.name, s.tools_json FROM tutor_agent_skills b JOIN skills s ON s.id=b.skill_id
		 WHERE b.agent_id=? AND b.class_id=? ORDER BY s.id`, agentID, classID)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	for rows.Next() {
		var skillID uint64
		var name string
		var raw []byte
		if err := rows.Scan(&skillID, &name, &raw); err != nil {
			return err
		}
		view.Skills = append(view.Skills, NamedItem{ID: skillID, Name: name})
		var tools []string
		if json.Unmarshal(raw, &tools) != nil {
			continue
		}
		for _, tool := range tools {
			if tool == "knowledge_search" {
				continue
			}
			if _, duplicated := seen[tool]; duplicated {
				continue
			}
			seen[tool] = struct{}{}
			view.ToolRefs = append(view.ToolRefs, tool)
		}
	}
	return rows.Err()
}

// ReplaceSkills 整体替换绑定的技能。任一编号不属于本班即按不存在处理，绑定关系不变。
func (s *Store) ReplaceSkills(ctx context.Context, classID, agentID uint64, skillIDs []uint64) error {
	return s.replaceBindings(ctx, classID, agentID, skillIDs,
		"DELETE FROM tutor_agent_skills WHERE agent_id=?",
		"INSERT INTO tutor_agent_skills (agent_id,skill_id,class_id) VALUES (?,?,?)",
		"SELECT COUNT(*) FROM skills WHERE id=? AND class_id=?")
}

// ReplaceMaterials 整体替换绑定的材料。任一编号不属于本班即按不存在处理。
func (s *Store) ReplaceMaterials(ctx context.Context, classID, agentID uint64, materialIDs []uint64) error {
	return s.replaceBindings(ctx, classID, agentID, materialIDs,
		"DELETE FROM tutor_agent_materials WHERE agent_id=?",
		"INSERT INTO tutor_agent_materials (agent_id,material_id,class_id) VALUES (?,?,?)",
		"SELECT COUNT(*) FROM materials WHERE id=? AND class_id=?")
}

func (s *Store) replaceBindings(ctx context.Context, classID, agentID uint64, ids []uint64, deleteSQL, insertSQL, existsSQL string) error {
	unique := make([]uint64, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return ErrNotFound
		}
		if _, duplicated := seen[id]; duplicated {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var agentExists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tutor_agents WHERE id=? AND class_id=?", agentID, classID).Scan(&agentExists); err != nil {
		return err
	}
	if agentExists == 0 {
		return ErrNotFound
	}
	for _, id := range unique {
		var exists int
		if err := tx.QueryRowContext(ctx, existsSQL, id, classID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
	}
	if _, err := tx.ExecContext(ctx, deleteSQL, agentID); err != nil {
		return err
	}
	for _, id := range unique {
		if _, err := tx.ExecContext(ctx, insertSQL, agentID, id, classID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BoundSkills 返回该智能体当前绑定且已启用的技能，按编号升序——路由依赖这个顺序做同分裁决。
func (s *Store) BoundSkills(ctx context.Context, classID, agentID uint64) ([]uint64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id FROM tutor_agent_skills b JOIN skills s ON s.id=b.skill_id
		 WHERE b.agent_id=? AND b.class_id=? AND s.enabled=1 ORDER BY s.id`, agentID, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uint64, 0, 4)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// BoundMaterials 返回该智能体绑定的材料编号，按编号升序。
func (s *Store) BoundMaterials(ctx context.Context, classID, agentID uint64) ([]uint64, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT material_id FROM tutor_agent_materials WHERE agent_id=? AND class_id=? ORDER BY material_id", agentID, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uint64, 0, 4)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// attachBindings 一次查出本班全部绑定再分发，避免逐个智能体查询。
func (s *Store) attachBindings(ctx context.Context, classID uint64, items []Row) error {
	if len(items) == 0 {
		return nil
	}
	index := make(map[uint64]*Row, len(items))
	for position := range items {
		items[position].SkillIDs = []uint64{}
		items[position].MaterialIDs = []uint64{}
		index[items[position].ID] = &items[position]
	}
	if err := s.eachBinding(ctx, classID, "SELECT agent_id,skill_id FROM tutor_agent_skills WHERE class_id=? ORDER BY agent_id,skill_id", index, true); err != nil {
		return err
	}
	return s.eachBinding(ctx, classID, "SELECT agent_id,material_id FROM tutor_agent_materials WHERE class_id=? ORDER BY agent_id,material_id", index, false)
}

func (s *Store) eachBinding(ctx context.Context, classID uint64, query string, index map[uint64]*Row, skills bool) error {
	rows, err := s.db.QueryContext(ctx, query, classID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var agentID, bindingID uint64
		if err := rows.Scan(&agentID, &bindingID); err != nil {
			return err
		}
		item, ok := index[agentID]
		if !ok {
			continue
		}
		if skills {
			item.SkillIDs = append(item.SkillIDs, bindingID)
		} else {
			item.MaterialIDs = append(item.MaterialIDs, bindingID)
		}
	}
	return rows.Err()
}

// ScopedMaterials 返回该智能体绑定材料中、标题含学科词的编号（按编号升序）。
// subject 为空表示不做学科过滤；标题比较用文件名部分，与页面显示一致。
func (s *Store) ScopedMaterials(ctx context.Context, classID, agentID uint64, subject string) ([]uint64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.original_name FROM tutor_agent_materials b JOIN materials m ON m.id=b.material_id
		 WHERE b.agent_id=? AND b.class_id=? ORDER BY m.id`, agentID, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uint64, 0, 4)
	for rows.Next() {
		var id uint64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if subject != "" && !strings.Contains(displayTitle(name), subject) {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// displayTitle 取文件名部分，与接口层展示材料标题的口径一致。
func displayTitle(name string) string {
	return path.Base(strings.ReplaceAll(name, "\\", "/"))
}

func validate(name, prompt string) error {
	if name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return fmt.Errorf("%w: 名称需为 1–%d 字", ErrInvalid, MaxNameRunes)
	}
	if utf8.RuneCountInString(prompt) > MaxPromptRunes {
		return fmt.Errorf("%w: 提示词不超过 %d 字", ErrInvalid, MaxPromptRunes)
	}
	return nil
}

// isDuplicate 只认唯一键冲突（1062）；名称重复时把它翻译成 ErrNameTaken。
func isDuplicate(err error) bool {
	var databaseError *mysql.MySQLError
	return errors.As(err, &databaseError) && databaseError.Number == 1062
}
