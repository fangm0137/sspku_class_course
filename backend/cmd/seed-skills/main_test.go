package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/fixtures"
	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/seed"
	"campusclaw/backend/internal/skill"
)

// 两份样板必须能被解析器接受，且字段与课件样板一致。
func TestSeededSkillFilesParse(t *testing.T) {
	seeds := seed.SeededSkills()
	if len(seeds) != 2 {
		t.Fatalf("expected two seeded skills, got %d", len(seeds))
	}
	want := map[string]struct {
		name    string
		subject string
		trigger string
	}{
		"skills/math-guidance.txt":    {"数学解题引导", "数学", "方程,去括号,分数,通分,面积,应用题,未知数,行程"},
		"skills/chinese-guidance.txt": {"语文解题引导", "语文", "春晓,默写,阅读,中心,作文,开头,古诗,词语"},
	}
	for _, item := range seeds {
		content, err := fixtures.Skills.ReadFile(item.Path)
		if err != nil {
			t.Fatalf("missing fixture %s", item.Path)
		}
		definition, err := skill.Parse(string(content))
		if err != nil {
			t.Fatalf("%s: %v", item.Path, err)
		}
		expected, ok := want[item.Path]
		if !ok {
			t.Fatalf("unexpected fixture %s", item.Path)
		}
		if definition.Name != expected.name || definition.SubjectText != expected.subject || definition.TriggerText != expected.trigger {
			t.Fatalf("%s parsed as %+v", item.Path, definition)
		}
		if !definition.Enabled || !reflect.DeepEqual(definition.Tools, []string{skill.KnowledgeSearchTool}) {
			t.Fatalf("%s must be enabled and use knowledge_search", item.Path)
		}
		if definition.Source != string(content) {
			t.Fatalf("%s must keep the original source text", item.Path)
		}
	}
}

func TestSeedSkillsRequiresDatabaseConfiguration(t *testing.T) {
	for _, missing := range []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD"} {
		env := map[string]string{
			"DB_HOST": "db.invalid", "DB_PORT": "3306", "DB_NAME": "campusclaw",
			"DB_USER": "campusclaw", "DB_PASSWORD": "test-only-password",
		}
		delete(env, missing)
		var out bytes.Buffer
		err := run(func(key string) string { return env[key] }, &out)
		if err == nil || !strings.Contains(err.Error(), missing) || out.Len() != 0 {
			t.Fatalf("missing %s must fail before any write, got %v", missing, err)
		}
	}
}

func TestSkillSeedIsIdempotentOnMySQL8(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run isolated verify-schema.ps1 -Suite seed")
	}
	cfg, err := config.LoadDatabase(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Name, "campusclaw_schema_verify_") {
		t.Fatal("requires isolated database")
	}
	pool, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pool.ExecContext(ctx, "INSERT INTO classes (name) VALUES ('A')"); err != nil {
		t.Fatal(err)
	}
	changed, err := seed.ApplySkills(ctx, pool, seed.SeededSkills())
	if err != nil || !changed {
		t.Fatalf("first run must insert: %v", err)
	}
	changed, err = seed.ApplySkills(ctx, pool, seed.SeededSkills())
	if err != nil || changed {
		t.Fatalf("second run must be a no-op: %v", err)
	}
	var count int
	if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM skills").Scan(&count); err != nil || count != 2 {
		t.Fatalf("expected two skill rows, got %d (%v)", count, err)
	}
	var subject sql.NullString
	var source string
	if err := pool.QueryRowContext(ctx, "SELECT subject_text,source_text FROM skills WHERE seed_key=?", "seed-skill-math-v1").Scan(&subject, &source); err != nil {
		t.Fatal(err)
	}
	if !subject.Valid || subject.String != "数学" || !strings.Contains(source, "数学解题引导") {
		t.Fatal("seeded row does not match the fixture")
	}
}
