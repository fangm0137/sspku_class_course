package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"campusclaw/backend/fixtures"
)

// AppSeed 描述一个内置 AI 应用：稳定内置键、名称、模板来源、技能与材料范围。
//
// 内置应用不是"特殊代码路径"：它只是 builtin_key 非空的一行 tutor_agents，
// 启停、绑定、提问都走与其他应用完全相同的接口。
type AppSeed struct {
	Key string
	// Name 是默认名称，教师可以改；幂等校验只在首次写入时用它。
	Name string
	// TemplateFile 与 TemplateName 决定它引用哪个模板。
	TemplateFile string
	TemplateName string
	// SkillNames 是初始绑定的技能（按名称查，避免依赖编号）。
	SkillNames []string
	// Subject 是初始绑定材料的标题过滤词；为空表示绑定本班全部材料。
	Subject string
}

// SeededApps 返回 A 班的三个内置应用。顺序稳定，便于验收与回溯。
func SeededApps() []AppSeed {
	return []AppSeed{
		{
			Key: "builtin-math", Name: "数学助教",
			TemplateFile: "apps/math.md", TemplateName: "数学助教模板",
			SkillNames: []string{"数学解题引导"}, Subject: "数学",
		},
		{
			Key: "builtin-chinese", Name: "语文助教",
			TemplateFile: "apps/chinese.md", TemplateName: "语文助教模板",
			SkillNames: []string{"语文解题引导"}, Subject: "语文",
		},
		{
			Key: "builtin-general", Name: "通用答疑",
			TemplateFile: "apps/general.md", TemplateName: "通用答疑模板",
			SkillNames: nil, Subject: "",
		},
	}
}

// ApplyApps 幂等地写入内置应用：首次插入模板、应用与绑定，之后只校验不改写。
//
// 与样板不一致时报冲突并停止，不覆盖教师改过的内容——教师可能正是为了演示才改的，
// 种子作业把它抹掉会让"我改了什么"永远说不清。
func ApplyApps(ctx context.Context, pool *sql.DB, seeds []AppSeed) (bool, error) {
	failure := errors.New("app seed initialization failed; inspect database before retrying")
	conflict := errors.New("app seed state conflicts with expected fixture")
	var classID uint64
	var count int
	if err := pool.QueryRowContext(ctx, "SELECT COUNT(*),MIN(id) FROM classes WHERE name=?", "A").Scan(&count, &classID); err != nil {
		return false, failure
	}
	if count != 1 {
		return false, errors.New("initialize a unique class A before app seeding")
	}
	changed := false
	for _, seed := range seeds {
		content, err := fixtures.Apps.ReadFile(seed.TemplateFile)
		if err != nil {
			return false, failure
		}
		body := strings.TrimSpace(string(content))
		inserted, err := applyApp(ctx, pool, classID, seed, body, failure, conflict)
		if err != nil {
			return false, err
		}
		if inserted {
			changed = true
		}
	}
	return changed, nil
}

func applyApp(ctx context.Context, pool *sql.DB, classID uint64, seed AppSeed, body string, failure, conflict error) (bool, error) {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return false, failure
	}
	defer tx.Rollback()

	templateID, insertedTemplate, err := ensureTemplate(ctx, tx, classID, seed.TemplateName, body, failure, conflict)
	if err != nil {
		return false, err
	}
	skillIDs, err := resolveSkills(ctx, tx, classID, seed.SkillNames, failure)
	if err != nil {
		return false, err
	}
	materialIDs, err := resolveMaterials(ctx, tx, classID, seed.Subject, failure)
	if err != nil {
		return false, err
	}

	var agentID uint64
	var name, prompt string
	var template sql.NullInt64
	var enabled bool
	err = tx.QueryRowContext(ctx,
		"SELECT id,name,prompt,prompt_template_id,enabled FROM tutor_agents WHERE builtin_key=?", seed.Key).
		Scan(&agentID, &name, &prompt, &template, &enabled)
	switch {
	case err == nil:
		// 已存在：逐项核对，任何一处不同都按冲突处理（不修、不覆盖）。
		if name != seed.Name || prompt != "" || !enabled {
			return false, conflict
		}
		if !template.Valid || uint64(template.Int64) != templateID {
			return false, conflict
		}
		boundSkills, err := boundIDs(ctx, tx, "SELECT skill_id FROM tutor_agent_skills WHERE agent_id=?", agentID, failure)
		if err != nil {
			return false, err
		}
		if !sameSet(boundSkills, skillIDs) {
			return false, conflict
		}
		boundMaterials, err := boundIDs(ctx, tx, "SELECT material_id FROM tutor_agent_materials WHERE agent_id=?", agentID, failure)
		if err != nil {
			return false, err
		}
		if !sameSet(boundMaterials, materialIDs) {
			return false, conflict
		}
	case errors.Is(err, sql.ErrNoRows):
		result, err := tx.ExecContext(ctx,
			`INSERT INTO tutor_agents (class_id,name,prompt,prompt_template_id,enabled,builtin_key)
			 VALUES (?,?,'',?,1,?)`, classID, seed.Name, templateID, seed.Key)
		if err != nil {
			return false, failure
		}
		newID, err := result.LastInsertId()
		if err != nil {
			return false, failure
		}
		agentID = uint64(newID)
		for _, skillID := range skillIDs {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO tutor_agent_skills (agent_id,skill_id,class_id) VALUES (?,?,?)",
				agentID, skillID, classID); err != nil {
				return false, failure
			}
		}
		for _, materialID := range materialIDs {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO tutor_agent_materials (agent_id,material_id,class_id) VALUES (?,?,?)",
				agentID, materialID, classID); err != nil {
				return false, failure
			}
		}
		if err := tx.Commit(); err != nil {
			return false, failure
		}
		return true, nil
	default:
		return false, failure
	}
	if err := tx.Commit(); err != nil {
		return false, failure
	}
	return insertedTemplate, nil
}

// ensureTemplate 按（班级, 名称）取或建模板。模板表没有 seed_key，
// 名称就是它在这里的幂等键；模板体与样板不一致同样算冲突。
func ensureTemplate(ctx context.Context, tx *sql.Tx, classID uint64, name, body string, failure, conflict error) (uint64, bool, error) {
	var id uint64
	var stored string
	err := tx.QueryRowContext(ctx,
		"SELECT id,body FROM prompt_templates WHERE class_id=? AND name=?", classID, name).Scan(&id, &stored)
	if err == nil {
		if strings.TrimSpace(stored) != body {
			return 0, false, conflict
		}
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, failure
	}
	result, err := tx.ExecContext(ctx,
		"INSERT INTO prompt_templates (class_id,name,body,description,enabled) VALUES (?,?,?,?,1)",
		classID, name, body, "内置应用默认模板")
	if err != nil {
		return 0, false, failure
	}
	newID, err := result.LastInsertId()
	if err != nil {
		return 0, false, failure
	}
	return uint64(newID), true, nil
}

func resolveSkills(ctx context.Context, tx *sql.Tx, classID uint64, names []string, failure error) ([]uint64, error) {
	ids := make([]uint64, 0, len(names))
	for _, name := range names {
		var id uint64
		err := tx.QueryRowContext(ctx, "SELECT id FROM skills WHERE class_id=? AND name=?", classID, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("initialize skill %q before app seeding", name)
		}
		if err != nil {
			return nil, failure
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// resolveMaterials 按标题过滤词挑材料；词为空表示全班材料都绑定。
// 没有匹配的材料时返回空集，应用照样可用（只是检索不到资料）。
func resolveMaterials(ctx context.Context, tx *sql.Tx, classID uint64, subject string, failure error) ([]uint64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,original_name FROM materials WHERE class_id=? ORDER BY id", classID)
	if err != nil {
		return nil, failure
	}
	defer rows.Close()
	ids := make([]uint64, 0, 4)
	for rows.Next() {
		var id uint64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, failure
		}
		if subject != "" && !strings.Contains(name, subject) {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func boundIDs(ctx context.Context, tx *sql.Tx, query string, agentID uint64, failure error) ([]uint64, error) {
	rows, err := tx.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, failure
	}
	defer rows.Close()
	ids := make([]uint64, 0, 4)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, failure
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// sameSet 比较两个编号集合，忽略顺序。
func sameSet(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	sortedLeft := append([]uint64(nil), left...)
	sortedRight := append([]uint64(nil), right...)
	sort.Slice(sortedLeft, func(i, j int) bool { return sortedLeft[i] < sortedLeft[j] })
	sort.Slice(sortedRight, func(i, j int) bool { return sortedRight[i] < sortedRight[j] })
	for index := range sortedLeft {
		if sortedLeft[index] != sortedRight[index] {
			return false
		}
	}
	return true
}
