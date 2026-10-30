package skill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

var (
	// ErrNotFound 覆盖"不存在"与"不属于本班"，调用方统一返回 404。
	ErrNotFound = errors.New("skill not found")
	// ErrNameTaken 表示班内名称重复。
	ErrNameTaken = errors.New("skill name already used in this class")
	// ErrBuiltin 表示这是平台自带的技能：可停用，不可删除（409）。
	ErrBuiltin = errors.New("builtin skill cannot be deleted")
)

// Detail 是技能记录加上供编辑回填的原文。
type Detail struct {
	Row
	SourceText string
}

// Store 是技能的存储层。
type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// selectColumns 固定列序，scanRows 与 Detail 都依赖它。
const selectColumns = "id,class_id,name,trigger_text,description,IFNULL(subject_text,''),enabled,tools_json,seed_key IS NOT NULL"

// List 返回本班技能，按编号升序（路由的同分裁决依赖这个顺序）。
func (s *Store) List(ctx context.Context, classID uint64) ([]Row, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+selectColumns+" FROM skills WHERE class_id=? ORDER BY id", classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// ListByIDs 读取本班指定的若干技能，只返回已启用的，按编号升序。
func (s *Store) ListByIDs(ctx context.Context, classID uint64, ids []uint64) ([]Row, error) {
	if len(ids) == 0 {
		return []Row{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, classID)
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+selectColumns+" FROM skills WHERE class_id=? AND enabled=1 AND id IN ("+strings.Join(placeholders, ",")+") ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// Detail 读取本班单个技能（含原文），跨班与不存在都返回 ErrNotFound。
func (s *Store) Detail(ctx context.Context, classID, id uint64) (Detail, error) {
	var item Detail
	var tools []byte
	err := s.db.QueryRowContext(ctx,
		"SELECT "+selectColumns+",source_text FROM skills WHERE id=? AND class_id=?", id, classID).
		Scan(&item.ID, &item.ClassID, &item.Name, &item.TriggerText, &item.Description, &item.SubjectText,
			&item.Enabled, &tools, &item.Builtin, &item.SourceText)
	if errors.Is(err, sql.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	item.Tools = decodeTools(tools)
	return item, nil
}

// Create 写入一条技能。调用方必须已经用 Parse 校验过原文。
func (s *Store) Create(ctx context.Context, classID uint64, definition Definition, seedKey any) (uint64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO skills (class_id,name,trigger_text,description,tools_json,source_text,subject_text,enabled,seed_key)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		classID, definition.Name, definition.TriggerText, definition.Description, definition.ToolsJSON(),
		definition.Source, nullable(definition.SubjectText), definition.Enabled, seedKey)
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

// Update 用新的文件原文整体替换该技能；名称冲突返回 ErrNameTaken。
func (s *Store) Update(ctx context.Context, classID, id uint64, definition Definition) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE skills SET name=?,trigger_text=?,description=?,tools_json=?,source_text=?,subject_text=?,enabled=?
		 WHERE id=? AND class_id=?`,
		definition.Name, definition.TriggerText, definition.Description, definition.ToolsJSON(),
		definition.Source, nullable(definition.SubjectText), definition.Enabled, id, classID)
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
		var exists int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM skills WHERE id=? AND class_id=?", id, classID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// Delete 删除本班技能；绑定关系由外键级联清理。
// 平台自带技能不可删除：它是"内置技能"这一能力的证据，误删即失去演示条件。
func (s *Store) Delete(ctx context.Context, classID, id uint64) error {
	var builtin bool
	err := s.db.QueryRowContext(ctx, "SELECT seed_key IS NOT NULL FROM skills WHERE id=? AND class_id=?", id, classID).Scan(&builtin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if builtin {
		return ErrBuiltin
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM skills WHERE id=? AND class_id=?", id, classID); err != nil {
		return err
	}
	return nil
}

func scanRows(rows *sql.Rows) ([]Row, error) {
	items := make([]Row, 0, 4)
	for rows.Next() {
		var item Row
		var tools []byte
		if err := rows.Scan(&item.ID, &item.ClassID, &item.Name, &item.TriggerText, &item.Description,
			&item.SubjectText, &item.Enabled, &tools, &item.Builtin); err != nil {
			return nil, err
		}
		item.Tools = decodeTools(tools)
		items = append(items, item)
	}
	return items, rows.Err()
}

// decodeTools 解出工具列表；解不开时退回"只认识内置工具"的保守结果。
func decodeTools(raw []byte) []string {
	var tools []string
	if err := json.Unmarshal(raw, &tools); err != nil {
		return []string{KnowledgeSearchTool}
	}
	return tools
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isDuplicate(err error) bool {
	var databaseError *mysql.MySQLError
	return errors.As(err, &databaseError) && databaseError.Number == 1062
}
