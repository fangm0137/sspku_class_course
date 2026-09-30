package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osExec "os/exec"
	"reflect"
	"strings"
	"testing"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

var seedKeys = []string{"SEED_TEACHER_A_PASSWORD", "SEED_STUDENT_A1_PASSWORD", "SEED_STUDENT_B1_PASSWORD"}

// Run the real command entry in a child process, with credentials only in its environment.
func TestSeedCommandProcess(t *testing.T) {
	if os.Getenv("CAMPUSCLAW_SEED_TEST_CHILD") != "1" {
		return
	}
	os.Args = os.Args[:1]
	main()
	os.Exit(0)
}

func TestMissingPasswordPrecedesDatabaseConfiguration(t *testing.T) {
	var out bytes.Buffer
	err := run(func(string) string { return "" }, &out)
	if err == nil || !strings.Contains(err.Error(), seedKeys[0]) || out.Len() != 0 {
		t.Fatal("seed passwords not checked first")
	}
}

func TestMySQL8Seed(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run verify-schema.ps1 -Suite seed")
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
	randomString := func() string {
		raw := make([]byte, 32)
		rand.Read(raw)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	passwords := []string{randomString(), randomString(), randomString()}
	env := map[string]string{}
	for i, key := range seedKeys {
		env[key] = passwords[i]
	}
	getenv := func(key string) string {
		if value, ok := env[key]; ok {
			return value
		}
		return os.Getenv(key)
	}
	count := func(table string) int {
		t.Helper()
		var n int
		// table names are only fixed test literals, never external input.
		if pool.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n) != nil {
			t.Fatal("count failed")
		}
		return n
	}
	execute := func(t *testing.T, want string) {
		t.Helper()
		binary, err := os.Executable()
		if err != nil {
			t.Fatal("test executable unavailable")
		}
		command := osExec.Command(binary, "-test.run=^TestSeedCommandProcess$")
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if _, replace := env[key]; !replace && key != "CAMPUSCLAW_SEED_TEST_CHILD" {
				command.Env = append(command.Env, entry)
			}
		}
		for key, value := range env {
			command.Env = append(command.Env, key+"="+value)
		}
		command.Env = append(command.Env, "CAMPUSCLAW_SEED_TEST_CHILD=1")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatal("initializer child process failed")
		}
		if string(out) != want+"\n" {
			t.Fatal("unexpected command output")
		}
		t.Log("seed:", strings.TrimSpace(string(out)))
	}
	t.Run("missing-passwords-no-writes", func(t *testing.T) {
		for _, key := range seedKeys {
			saved := env[key]
			env[key] = ""
			var out bytes.Buffer
			err := run(getenv, &out)
			env[key] = saved
			if err == nil || !strings.Contains(err.Error(), key) || out.Len() != 0 || count("classes") != 0 || count("users") != 0 {
				t.Fatal("missing password wrote data or reported success")
			}
		}
	})
	type account struct {
		ID, ClassID                 uint64
		Username, Role, Class, Hash string
	}
	snapshot := func() []account {
		t.Helper()
		rows, err := pool.Query("SELECT u.id,u.class_id,u.username,u.role,c.name,u.password_hash FROM users u JOIN classes c ON c.id=u.class_id ORDER BY u.username")
		if err != nil {
			t.Fatal("snapshot failed")
		}
		defer rows.Close()
		var result []account
		for rows.Next() {
			var a account
			if rows.Scan(&a.ID, &a.ClassID, &a.Username, &a.Role, &a.Class, &a.Hash) != nil {
				t.Fatal("scan failed")
			}
			result = append(result, a)
		}
		if rows.Err() != nil {
			t.Fatal("rows failed")
		}
		return result
	}
	t.Run("first-initialization", func(t *testing.T) {
		execute(t, "accounts initialized")
		if count("classes") != 2 || count("users") != 3 {
			t.Fatal("wrong fixture counts")
		}
		// Authentication is stateless: migration 000002 dropped the session table.
		for _, table := range []string{"materials", "knowledge_entries"} {
			if count(table) != 0 {
				t.Fatal("seed created out-of-scope data")
			}
		}
		var sessions int
		if pool.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='sessions'").Scan(&sessions) != nil || sessions != 0 {
			t.Fatal("session table must not exist")
		}
		want := map[string][2]string{"teacher_a": {"teacher", "A"}, "student_a1": {"student", "A"}, "student_b1": {"student", "B"}}
		salts := map[string]bool{}
		for _, a := range snapshot() {
			if pair, ok := want[a.Username]; !ok || pair != [2]string{a.Role, a.Class} {
				t.Fatal("wrong fixture identity")
			}
			parts := strings.Split(a.Hash, "$")
			if len(parts) != 7 || !strings.HasPrefix(a.Hash, "$scrypt$32768$8$1$") || salts[parts[5]] {
				t.Fatal("bad hash or reused salt")
			}
			salts[parts[5]] = true
			for _, p := range passwords {
				if strings.Contains(a.Hash, p) {
					t.Fatal("plaintext persisted")
				}
			}
		}
	})
	initial := snapshot()
	t.Run("serial-repeat-preserves-ids-and-hashes", func(t *testing.T) {
		execute(t, "no change")
		for _, key := range seedKeys {
			env[key] = randomString()
		}
		execute(t, "no change")
		if count("classes") != 2 || count("users") != 3 || !reflect.DeepEqual(initial, snapshot()) {
			t.Fatal("repeat modified existing data")
		}
		env[seedKeys[2]] = ""
		var out bytes.Buffer
		if run(getenv, &out) == nil || out.Len() != 0 || !reflect.DeepEqual(initial, snapshot()) {
			t.Fatal("repeat without passwords accepted")
		}
		env[seedKeys[2]] = randomString()
	})
	t.Run("seed-passwords-work-through-login", func(t *testing.T) {
		handler := httpapi.NewHandler(pool, randomString())
		users := []string{"teacher_a", "student_a1", "student_b1"}
		for i, username := range users {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/csrf", nil))
			if w.Code != 200 || len(w.Result().Cookies()) != 1 {
				t.Fatal("CSRF failed")
			}
			cookie := w.Result().Cookies()[0]
			var token map[string]string
			json.Unmarshal(w.Body.Bytes(), &token)
			login := func(password string) *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]string{"username": username, "password": password})
				r := httptest.NewRequest("POST", "http://localhost/api/login", bytes.NewReader(body))
				r.AddCookie(cookie)
				r.Header.Set("Origin", "http://localhost")
				r.Header.Set("X-CSRF-Token", token["csrf_token"])
				r.Header.Set("Content-Type", "application/json")
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, r)
				return out
			}
			if login(env[seedKeys[i]]).Code != 401 {
				t.Fatal("repeat reset password")
			}
			response := login(passwords[i])
			if response.Code != http.StatusOK {
				t.Fatal("original seed password failed")
			}
			var identity struct {
				User struct {
					Username, Role string
					ClassID        uint64 `json:"class_id"`
				} `json:"user"`
			}
			if json.Unmarshal(response.Body.Bytes(), &identity) != nil || identity.User.Username != username {
				t.Fatal("login identity wrong")
			}
		}
	})
	exec := func(query string, args ...any) sql.Result {
		t.Helper()
		res, err := pool.Exec(query, args...)
		if err != nil {
			t.Fatal("fixture SQL failed")
		}
		return res
	}
	t.Run("conflicts-do-not-overwrite", func(t *testing.T) {
		exec("UPDATE users SET role=? WHERE username=?", "student", "teacher_a")
		conflicting := snapshot()
		var out bytes.Buffer
		if run(getenv, &out) == nil || out.Len() != 0 || !reflect.DeepEqual(conflicting, snapshot()) {
			t.Fatal("account conflict overwritten")
		}
		exec("UPDATE users SET role=? WHERE username=?", "teacher", "teacher_a")
		extra := exec("INSERT INTO classes (name) VALUES (?)", "A")
		id, _ := extra.LastInsertId()
		if run(getenv, &out) == nil || count("classes") != 3 || !reflect.DeepEqual(initial, snapshot()) {
			t.Fatal("duplicate class not rejected")
		}
		exec("DELETE FROM classes WHERE id=?", id)
	})
	t.Log("PASS: 2 classes, 3 accounts; repeated initialization preserves IDs and hashes; original passwords still authenticate")
}
