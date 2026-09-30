package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"os"
	osExec "os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"campusclaw/backend/fixtures"
	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/seed"
)

func TestSamplesAndConfiguration(t *testing.T) {
	names := []string{"class-a-intro.txt", "class-a-notes.md", "class-b-seed.txt"}
	markers := []string{"CLASS_A_TXT_ONLY", "CLASS_A_MD_ONLY", "CLASS_B_SEED_ONLY"}
	for i, name := range names {
		content, err := fixtures.Files.ReadFile(name)
		if err != nil || !utf8.Valid(content) || bytes.Contains(content, []byte("\r")) {
			t.Fatal("sample encoding invalid")
		}
		for j, marker := range markers {
			if bytes.Contains(content, []byte(marker)) != (i == j) {
				t.Fatal("sample markers not isolated")
			}
		}
		if i == 1 && !bytes.HasPrefix(content, []byte("# A notes\n")) {
			t.Fatal("markdown header missing")
		}
	}
	for _, bad := range []string{"", " ", "relative/uploads"} {
		var out bytes.Buffer
		if err := run(func(string) string { return bad }, &out); err == nil || !strings.Contains(err.Error(), "UPLOAD_DIR") || out.Len() != 0 {
			t.Fatal("invalid path accepted")
		}
	}
}

func TestMaterialSeedProcess(t *testing.T) {
	if os.Getenv("CAMPUSCLAW_MATERIAL_TEST_CHILD") != "1" {
		return
	}
	os.Args = os.Args[:1]
	main()
	os.Exit(0)
}

func TestMySQL8MaterialSeed(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run verify-schema.ps1 -Suite materials")
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
	var version string
	if pool.QueryRow("SELECT VERSION()").Scan(&version) != nil || !strings.HasPrefix(version, "8.0.") {
		t.Fatal("requires MySQL 8.0")
	}
	t.Log("MySQL", version)
	directory := filepath.Join(t.TempDir(), "uploads")
	getenv := func(key string) string {
		if key == "UPLOAD_DIR" {
			return directory
		}
		return os.Getenv(key)
	}
	exec := func(t *testing.T, query string, args ...any) sql.Result {
		t.Helper()
		res, err := pool.Exec(query, args...)
		if err != nil {
			t.Fatal("fixture SQL failed")
		}
		return res
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if pool.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n) != nil {
			t.Fatal("count failed")
		}
		return n
	}
	t.Run("missing-class-no-file-or-record", func(t *testing.T) {
		var out bytes.Buffer
		if run(getenv, &out) == nil || out.Len() != 0 || count("materials") != 0 {
			t.Fatal("missing B class accepted")
		}
		if _, err := os.Stat(directory); !os.IsNotExist(err) {
			t.Fatal("missing B wrote storage")
		}
	})
	raw := make([]byte, 32)
	rand.Read(raw)
	password := base64.RawURLEncoding.EncodeToString(raw)
	passwords, err := seed.LoadPasswords(func(string) string { return password })
	if err != nil {
		t.Fatal("password fixture failed")
	}
	if _, err := seed.Apply(context.Background(), pool, passwords); err != nil {
		t.Fatal("account fixture failed")
	}
	var a, b, teacher int64
	if pool.QueryRow("SELECT id FROM classes WHERE name=?", "A").Scan(&a) != nil || pool.QueryRow("SELECT id FROM classes WHERE name=?", "B").Scan(&b) != nil || pool.QueryRow("SELECT id FROM users WHERE username=?", "teacher_a").Scan(&teacher) != nil {
		t.Fatal("identity fixture failed")
	}
	// Independent A fixture for preservation checks, not a product upload implementation.
	aContent, _ := fixtures.Files.ReadFile("class-a-intro.txt")
	result := exec(t, "INSERT INTO materials (class_id,uploader_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?,?)", a, teacher, "class-a-intro.txt", "existing-a-test.txt", len(aContent), "upload")
	aid, _ := result.LastInsertId()
	exec(t, "INSERT INTO knowledge_entries (material_id,class_id,extracted_text) VALUES (?,?,?)", aid, a, string(aContent))
	if os.MkdirAll(directory, 0700) != nil || os.WriteFile(filepath.Join(directory, "existing-a-test.txt"), aContent, 0600) != nil {
		t.Fatal("A file fixture failed")
	}
	execute := func(t *testing.T, want string) {
		t.Helper()
		binary, err := os.Executable()
		if err != nil {
			t.Fatal("test executable unavailable")
		}
		cmd := osExec.Command(binary, "-test.run=^TestMaterialSeedProcess$")
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if key != "UPLOAD_DIR" && key != "CAMPUSCLAW_MATERIAL_TEST_CHILD" {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "UPLOAD_DIR="+directory, "CAMPUSCLAW_MATERIAL_TEST_CHILD=1")
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != want+"\n" {
			t.Fatal("material command failed or output invalid")
		}
		t.Log("seed-materials:", strings.TrimSpace(string(output)))
	}
	type material struct {
		ID, KID, ClassID, KClassID uint64
		Uploader                   sql.NullInt64
		Name, Key, Source, Text    string
		Size                       int64
		Created                    time.Time
	}
	snapshot := func() []material {
		t.Helper()
		rows, err := pool.Query("SELECT m.id,k.id,m.class_id,k.class_id,m.uploader_id,m.original_name,m.storage_key,m.source,m.size,m.created_at,k.extracted_text FROM materials m JOIN knowledge_entries k ON k.material_id=m.id ORDER BY m.id")
		if err != nil {
			t.Fatal("snapshot failed")
		}
		defer rows.Close()
		var all []material
		for rows.Next() {
			var v material
			if rows.Scan(&v.ID, &v.KID, &v.ClassID, &v.KClassID, &v.Uploader, &v.Name, &v.Key, &v.Source, &v.Size, &v.Created, &v.Text) != nil {
				t.Fatal("snapshot scan failed")
			}
			all = append(all, v)
		}
		if rows.Err() != nil {
			t.Fatal("snapshot rows failed")
		}
		return all
	}
	beforeA := snapshot()[0]
	content, _ := fixtures.Files.ReadFile("class-b-seed.txt")
	path := filepath.Join(directory, seed.MaterialKey)
	t.Run("complete-B-seed-and-A-preservation", func(t *testing.T) {
		execute(t, "material seed initialized")
		all := snapshot()
		if count("materials") != 2 || count("knowledge_entries") != 2 || len(all) != 2 || !reflect.DeepEqual(all[0], beforeA) {
			t.Fatal("wrong counts or A modified")
		}
		v := all[1]
		if v.ClassID != uint64(b) || v.KClassID != v.ClassID || v.Uploader.Valid || v.Source != "seed" || v.Name != "class-b-seed.txt" || v.Key != seed.MaterialKey || v.Size != int64(len(content)) || v.Text != string(content) {
			t.Fatal("B seed mismatch")
		}
		file, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(file) != sha256.Sum256(content) {
			t.Fatal("B original bytes mismatch")
		}
		file, err = os.ReadFile(filepath.Join(directory, "existing-a-test.txt"))
		if err != nil || !bytes.Equal(file, aContent) {
			t.Fatal("A file modified")
		}
		if count("users") != 3 {
			t.Fatal("extra user created")
		}
	})
	initial := snapshot()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal("B file missing")
	}
	t.Run("serial-repeat-preserves-records-and-file", func(t *testing.T) {
		execute(t, "no change")
		execute(t, "no change")
		current, err := os.Stat(path)
		if err != nil || !current.ModTime().Equal(info.ModTime()) || !reflect.DeepEqual(initial, snapshot()) || count("materials") != 2 || count("knowledge_entries") != 2 {
			t.Fatal("repeat changed seed")
		}
		files, err := os.ReadDir(directory)
		if err != nil || len(files) != 2 {
			t.Fatal("repeat duplicated files")
		}
	})
	t.Run("inconsistent-state-not-overwritten", func(t *testing.T) {
		if os.WriteFile(path, []byte("test conflicting content"), 0600) != nil {
			t.Fatal("fault setup failed")
		}
		var out bytes.Buffer
		if run(getenv, &out) == nil || out.Len() != 0 || !reflect.DeepEqual(initial, snapshot()) {
			t.Fatal("inconsistency overwritten")
		}
		actual, _ := os.ReadFile(path)
		if string(actual) != "test conflicting content" {
			t.Fatal("conflicting file replaced")
		}
		if os.Remove(path) != nil {
			t.Fatal("test file removal failed")
		}
		if run(getenv, &out) == nil {
			t.Fatal("missing file silently repaired")
		}
		if os.WriteFile(path, content, 0600) != nil {
			t.Fatal("fixture restore failed")
		}
	})
	t.Log("PASS: distinct A TXT/MD samples; B seed original and text match; repeated initialization preserves IDs, timestamps and file; no extra teacher")
}
