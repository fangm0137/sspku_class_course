package prompt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

// 长度上限与数据库 CHECK 保持一致，两者任一被突破都算配置错误。
const (
	MaxNameRunes = 64
	MinBodyRunes = 1
	MaxBodyRunes = 4000
	MaxDescRunes = 255
)

var (
	// ErrNotFound 覆盖"不存在"与"不属于本班"，调用方统一返回 404。
	ErrNotFound = errors.New("prompt template not found")
	// ErrNameTaken 表示班内名称重复。
	ErrNameTaken = errors.New("template name already used in this class")
	// ErrInvalid 表示字段不满足长度或必填约束。
	ErrInvalid = errors.New("invalid template field")
	// ErrReferenced 表示模板仍被应用引用，删除被拒绝（409）。
	ErrReferenced = errors.New("template still referenced by an application")
)

// Row 是一个提示词模板。
type Row struct {
	ID          uint64
	ClassID     uint64
	Name        string
	Body        string
	Description string
	Enabled     bool
}

// Store 是模板的存储层。班级一律由调用方从登录用户派生。
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// List 返回本班模板；includeDisabled 为假时只返回已启用的（学生视角）。
func (s *Store) List(ctx context.Context, classID uint64, includeDisabled bool) ([]Row, error) {
	query := "SELECT id,class_id,name,body,IFNULL(description,''),enabled FROM prompt_templates WHERE class_id=?"
	if !includeDisabled {
		query += " AND enabled=1"
	}
	query += " ORDER BY id"
	rows, err := s.db.QueryContext(ctx, query, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Row, 0, 4)
	for rows.Next() {
		var item Row
		if err := rows.Scan(&item.ID, &item.ClassID, &item.Name, &item.Body, &item.Description, &item.Enabled); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Get 读取本班单个模板；跨班与不存在都返回 ErrNotFound。
func (s *Store) Get(ctx context.Context, classID, id uint64) (Row, error) {
	var item Row
	err := s.db.QueryRowContext(ctx,
		"SELECT id,class_id,name,body,IFNULL(description,''),enabled FROM prompt_templates WHERE id=? AND class_id=?",
		id, classID).
		Scan(&item.ID, &item.ClassID, &item.Name, &item.Body, &item.Description, &item.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, err
	}
	return item, nil
}

// Create 新建模板。班内重名由唯一键兜底，冲突返回 ErrNameTaken。
func (s *Store) Create(ctx context.Context, classID uint64, name, body, description string, enabled bool) (uint64, error) {
	name, body, description = normalize(name, body, description)
	if err := validate(name, body, description); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO prompt_templates (class_id,name,body,description,enabled) VALUES (?,?,?,?,?)",
		classID, name, body, nullable(description), enabled)
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

// Update 整体覆盖名称、模板体、说明与启用状态。
func (s *Store) Update(ctx context.Context, classID, id uint64, name, body, description string, enabled bool) error {
	name, body, description = normalize(name, body, description)
	if err := validate(name, body, description); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE prompt_templates SET name=?,body=?,description=?,enabled=? WHERE id=? AND class_id=?",
		name, body, nullable(description), enabled, id, classID)
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
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM prompt_templates WHERE id=? AND class_id=?", id, classID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// Delete 删除模板；仍被应用引用时返回 ErrReferenced，不做级联清空——
// 悄悄把应用改成默认角色会让"这个应用为什么这么回答"失去解释。
func (s *Store) Delete(ctx context.Context, classID, id uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM prompt_templates WHERE id=? AND class_id=?", id, classID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	var referenced int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tutor_agents WHERE prompt_template_id=?", id).Scan(&referenced); err != nil {
		return err
	}
	if referenced > 0 {
		return ErrReferenced
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM prompt_templates WHERE id=? AND class_id=?", id, classID); err != nil {
		return err
	}
	return tx.Commit()
}

func normalize(name, body, description string) (string, string, string) {
	return strings.TrimSpace(name), strings.TrimSpace(body), strings.TrimSpace(description)
}

func validate(name, body, description string) error {
	if name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return fmt.Errorf("%w: 名称需为 1–%d 字", ErrInvalid, MaxNameRunes)
	}
	length := utf8.RuneCountInString(body)
	if strings.TrimSpace(body) == "" || length < MinBodyRunes || length > MaxBodyRunes {
		return fmt.Errorf("%w: 模板体需为 %d–%d 字", ErrInvalid, MinBodyRunes, MaxBodyRunes)
	}
	if strings.ContainsRune(body, '\x00') || strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("%w: 内容包含非法字符", ErrInvalid)
	}
	if utf8.RuneCountInString(description) > MaxDescRunes {
		return fmt.Errorf("%w: 说明不超过 %d 字", ErrInvalid, MaxDescRunes)
	}
	return nil
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
