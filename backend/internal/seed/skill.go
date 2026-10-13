package seed

import (
	"context"
	"database/sql"
	"errors"

	"campusclaw/backend/fixtures"
	"campusclaw/backend/internal/skill"
)

// SkillSeed 描述一份预置技能：嵌入文件路径、幂等键与所属班级名。
type SkillSeed struct {
	Path  string
	Key   string
	Class string
}

// SeededSkills 返回 A 班的两份样板技能。顺序稳定，便于验收与回溯。
func SeededSkills() []SkillSeed {
	return []SkillSeed{
		{Path: "skills/math-guidance.txt", Key: "seed-skill-math-v1", Class: "A"},
		{Path: "skills/chinese-guidance.txt", Key: "seed-skill-chinese-v1", Class: "A"},
	}
}

// ApplySkills 幂等地写入预置技能：首次插入，之后只校验不改写。
// 与样板不一致时报冲突而不是静默覆盖——教师改过的技能不该被种子作业抹掉。
func ApplySkills(ctx context.Context, pool *sql.DB, seeds []SkillSeed) (bool, error) {
	failure := errors.New("skill seed initialization failed; inspect database before retrying")
	conflict := errors.New("skill seed state conflicts with expected fixture")
	changed := false
	for _, seed := range seeds {
		content, err := fixtures.Skills.ReadFile(seed.Path)
		if err != nil {
			return false, failure
		}
		definition, err := skill.Parse(string(content))
		if err != nil {
			return false, failure
		}
		inserted, err := applySkill(ctx, pool, seed, definition, failure, conflict)
		if err != nil {
			return false, err
		}
		if inserted {
			changed = true
		}
	}
	return changed, nil
}

func applySkill(ctx context.Context, pool *sql.DB, seed SkillSeed, definition skill.Definition, failure, conflict error) (bool, error) {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return false, failure
	}
	defer tx.Rollback()
	var count int
	var classID uint64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),MIN(id) FROM classes WHERE name=?", seed.Class).Scan(&count, &classID); err != nil {
		return false, failure
	}
	if count != 1 {
		return false, errors.New("initialize a unique class " + seed.Class + " before skill seeding")
	}
	var (
		id                                  uint64
		name, trigger, description, subject string
		enabled                             bool
	)
	err = tx.QueryRowContext(ctx,
		"SELECT id,name,trigger_text,description,IFNULL(subject_text,''),enabled FROM skills WHERE seed_key=?", seed.Key).
		Scan(&id, &name, &trigger, &description, &subject, &enabled)
	switch {
	case err == nil:
		if name != definition.Name || trigger != definition.TriggerText || description != definition.Description ||
			subject != definition.SubjectText || enabled != definition.Enabled {
			return false, conflict
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO skills (class_id,name,trigger_text,description,tools_json,source_text,subject_text,enabled,seed_key)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			classID, definition.Name, definition.TriggerText, definition.Description, definition.ToolsJSON(),
			definition.Source, nullableText(definition.SubjectText), definition.Enabled, seed.Key); err != nil {
			return false, failure
		}
		if err = tx.Commit(); err != nil {
			return false, failure
		}
		return true, nil
	default:
		return false, failure
	}
	if err = tx.Commit(); err != nil {
		return false, failure
	}
	return false, nil
}

// nullableText 让空字符串入库为 NULL，保持"未设置资料范围"与"设置了空值"可区分。
func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
