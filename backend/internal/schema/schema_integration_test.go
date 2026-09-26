package schema

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/scrypt"
)

func TestMySQL8Schema(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("set TEST_MYSQL8=1 and DB_* for an isolated empty MySQL 8.0 test database")
	}
	cfg, err := config.LoadDatabase(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Name, "campusclaw_schema_verify_") {
		t.Fatal("requires an isolated campusclaw_schema_verify_ database")
	}
	pool, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var version string
	if err := pool.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal("cannot read server version")
	}
	if !strings.HasPrefix(version, "8.0.") {
		t.Fatalf("expected MySQL 8.0, got %s", version)
	}
	t.Logf("SELECT VERSION(): %s", version)
	for n := 1; n <= 2; n++ {
		changed, err := Up(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Fatal("CLI should have applied migration before integration verification")
		}
		t.Logf("repeat Up %d: no change", n)
	}
	rows, err := pool.Query("SHOW TABLES")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	expected := []string{"classes", "knowledge_entries", "materials", "schema_migrations", "sessions", "users"}
	if !reflect.DeepEqual(tables, expected) {
		t.Fatalf("unexpected tables: %v", tables)
	}
	t.Logf("SHOW TABLES: %s", strings.Join(tables, ", "))
	for _, table := range []string{"materials", "knowledge_entries"} {
		var name, ddl string
		// Table identifiers are fixed allowlisted constants, never external input.
		if err := pool.QueryRow("SHOW CREATE TABLE "+table).Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		t.Logf("SHOW CREATE TABLE %s:\n%s", name, ddl)
		var nullable string
		if err := pool.QueryRow("SELECT IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND COLUMN_NAME='class_id'", cfg.Name, table).Scan(&nullable); err != nil || nullable != "NO" {
			t.Fatal("class_id must be NOT NULL")
		}
		var indexes int
		if err := pool.QueryRow("SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND COLUMN_NAME='class_id' AND SEQ_IN_INDEX=1", cfg.Name, table).Scan(&indexes); err != nil || indexes == 0 {
			t.Fatal("class_id needs a leading index")
		}
	}
	for _, table := range []string{"classes", "users", "sessions", "materials", "knowledge_entries"} {
		var count int
		if err := pool.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s must be empty; migrations must not seed data", table)
		}
	}
	t.Log("all five business tables empty after migration")
	tx, err := pool.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	execID := func(query string, args ...any) int64 {
		r, err := tx.Exec(query, args...)
		if err != nil {
			t.Fatalf("fixture insert failed (%T)", err)
		}
		id, err := r.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	reject := func(label string, code uint16, query string, args ...any) {
		_, err := tx.Exec(query, args...)
		var mysqlErr *mysql.MySQLError
		if !errors.As(err, &mysqlErr) || mysqlErr.Number != code {
			t.Fatalf("%s: expected MySQL error %d, got %T", label, code, err)
		}
		t.Logf("PASS %s: MySQL error %d", label, mysqlErr.Number)
	}
	a := execID("INSERT INTO classes(name) VALUES (?)", "fixture-a")
	b := execID("INSERT INTO classes(name) VALUES (?)", "fixture-b")
	password := make([]byte, 24)
	salt := make([]byte, 16)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	key, err := scrypt.Key(password, salt, 16384, 8, 1, 32)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("$scrypt$ln=14,r=8,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	u := execID("INSERT INTO users(username,password_hash,role,class_id) VALUES (?,?,?,?)", "fixture-user", hash, "teacher", a)
	var stored string
	if err := tx.QueryRow("SELECT password_hash FROM users WHERE id=?", u).Scan(&stored); err != nil || stored != hash || stored == string(password) {
		t.Fatal("hash storage mismatch")
	}
	t.Log("PASS scrypt hash stored; plaintext never inserted")
	reject("duplicate username", 1062, "INSERT INTO users(username,password_hash,role,class_id) VALUES (?,?,?,?)", "fixture-user", hash, "student", a)
	reject("plaintext password format", 3819, "INSERT INTO users(username,password_hash,role,class_id) VALUES (?,?,?,?)", "invalid-hash", "not-a-password-hash", "student", a)
	reject("user class foreign key", 1452, "INSERT INTO users(username,password_hash,role,class_id) VALUES (?,?,?,?)", "invalid-class", hash, "student", 999999)
	reject("role enum", 1265, "INSERT INTO users(username,password_hash,role,class_id) VALUES (?,?,?,?)", "invalid-role", hash, "admin", a)
	mac := hmac.New(sha256.New, salt)
	mac.Write(password)
	digest := mac.Sum(nil)
	now := time.Now().UTC()
	execID("INSERT INTO sessions(session_id_hash,user_id,created_at,expires_at) VALUES (?,?,?,?)", digest, nil, now, now.Add(30*time.Minute))
	t.Log("PASS anonymous session accepts NULL user_id and 32-byte HMAC digest")
	reject("duplicate session digest", 1062, "INSERT INTO sessions(session_id_hash,user_id,created_at,expires_at) VALUES (?,?,?,?)", digest, u, now, now.Add(time.Hour))
	other := sha256.Sum256(digest)
	reject("session user foreign key", 1452, "INSERT INTO sessions(session_id_hash,user_id,created_at,expires_at) VALUES (?,?,?,?)", other[:], 999999, now, now.Add(time.Hour))
	m := execID("INSERT INTO materials(class_id,uploader_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?,?)", a, u, "fixture.txt", "fixture-key", 7, "upload")
	seed := execID("INSERT INTO materials(class_id,uploader_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?,?)", b, nil, "fixture-b.txt", "fixture-seed", 7, "seed")
	reject("materials class NOT NULL", 1048, "INSERT INTO materials(class_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?)", nil, "bad.txt", "bad-null", 1, "seed")
	reject("materials class foreign key", 1452, "INSERT INTO materials(class_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?)", 999999, "bad.txt", "bad-class", 1, "seed")
	reject("duplicate storage key", 1062, "INSERT INTO materials(class_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?)", a, "bad.txt", "fixture-key", 1, "seed")
	reject("source enum", 1265, "INSERT INTO materials(class_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?)", a, "bad.txt", "bad-source", 1, "other")
	reject("upload requires uploader", 3819, "INSERT INTO materials(class_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?)", a, "bad.txt", "bad-uploader", 1, "upload")
	execID("INSERT INTO knowledge_entries(material_id,class_id,extracted_text) VALUES (?,?,?)", m, a, "fixture text")
	reject("duplicate material knowledge link", 1062, "INSERT INTO knowledge_entries(material_id,class_id,extracted_text) VALUES (?,?,?)", m, a, "duplicate")
	reject("knowledge class NOT NULL", 1048, "INSERT INTO knowledge_entries(material_id,class_id,extracted_text) VALUES (?,?,?)", seed, nil, "invalid")
	reject("knowledge material foreign key", 1452, "INSERT INTO knowledge_entries(material_id,class_id,extracted_text) VALUES (?,?,?)", 999999, a, "invalid")
	reject("knowledge class must match material", 1452, "INSERT INTO knowledge_entries(material_id,class_id,extracted_text) VALUES (?,?,?)", seed, a, "invalid")
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"classes", "users", "sessions", "materials", "knowledge_entries"} {
		var count int
		if err := pool.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("fixture rollback failed: %s", table)
		}
	}
	t.Log("PASS transaction rollback: all five business tables empty")
}
