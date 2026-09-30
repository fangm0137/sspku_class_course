package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
)

func TestMySQL8Session(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run isolated verify-schema.ps1 -Suite session")
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
	_, secret := randomValue()
	handler := NewHandler(pool, secret)
	security := NewSecurity(pool, secret)
	exec := func(t *testing.T, q string, args ...any) sql.Result {
		t.Helper()
		res, err := pool.Exec(q, args...)
		if err != nil {
			t.Fatal("fixture SQL failed")
		}
		return res
	}
	classA, _ := exec(t, "INSERT INTO classes (name) VALUES (?)", "session-test-A").LastInsertId()
	classB, _ := exec(t, "INSERT INTO classes (name) VALUES (?)", "session-test-B").LastInsertId()
	_, password := randomValue()
	hash := fixturePasswordHash(t, password)
	for _, role := range []string{"teacher", "student"} {
		exec(t, "INSERT INTO users (username,password_hash,role,class_id) VALUES (?,?,?,?)", "session-"+role, hash, role, classA)
	}
	// Identity now travels in the Authorization header; there is no cookie and no
	// write-protection token endpoint.
	request := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	checkError := func(t *testing.T, w *httptest.ResponseRecorder, status int) {
		t.Helper()
		var obj map[string]string
		codes := map[int]string{401: "unauthenticated", 404: "not_found", 503: "service_unavailable"}
		messages := map[int]string{401: "请先登录", 404: "资源不存在", 503: "服务暂不可用"}
		if w.Code != status || json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 2 || obj["error"] != codes[status] || obj["message"] != messages[status] || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("unsafe failure response")
		}
	}
	login := func(t *testing.T, role string) string {
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost:8080/api/login", strings.NewReader(loginJSON("session-"+role, password)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || len(w.Result().Cookies()) != 0 {
			t.Fatal("login prerequisite failed")
		}
		var obj struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(w.Body.Bytes(), &obj) != nil || obj.Token == "" {
			t.Fatal("login did not return a token")
		}
		return obj.Token
	}
	userID := func(t *testing.T, username string) uint64 {
		t.Helper()
		var id uint64
		if pool.QueryRow("SELECT id FROM users WHERE username=?", username).Scan(&id) != nil {
			t.Fatal("fixture identity lookup failed")
		}
		return id
	}
	const base = "http://localhost:8080"
	t.Run("teacher-student-me-latest-identity", func(t *testing.T) {
		for _, role := range []string{"teacher", "student"} {
			token := login(t, role)
			w := request("GET", base+"/api/me?role=admin&class_id=999", token)
			var obj map[string]json.RawMessage
			var got userIdentity
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 4 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID == 0 || got.Username != "session-"+role || got.Role != role || got.ClassID != uint64(classA) || w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
				t.Fatal("identity contract failed")
			}
			if strings.Contains(w.Body.String(), token) {
				t.Fatal("me must not echo the token back")
			}
			exec(t, "UPDATE users SET class_id=? WHERE id=?", classB, got.ID)
			w = request("GET", base+"/api/me", token)
			json.Unmarshal(w.Body.Bytes(), &got)
			if w.Code != 200 || got.ClassID != uint64(classB) {
				t.Fatal("me used stale identity")
			}
			exec(t, "UPDATE users SET class_id=? WHERE id=?", classA, got.ID)
		}
	})
	t.Run("anonymous-invalid-expired-token", func(t *testing.T) {
		_, unknown := randomValue()
		expiredUser := userIdentity{ID: userID(t, "session-student"), Username: "session-student", Role: "student", ClassID: uint64(classA)}
		expired, _ := security.issueToken(expiredUser, time.Now().UTC().Add(-9*time.Hour))
		if expired == "" {
			t.Fatal("cannot build an expired fixture token")
		}
		for _, token := range []string{"", "invalid", "a.b.c", unknown, expired} {
			checkError(t, request("GET", base+"/api/me", token), 401)
			checkError(t, request("POST", base+"/api/logout", token), 401)
		}
		checkError(t, request("GET", base+"/api/csrf", ""), 404)
		checkError(t, request("GET", base+"/api/csrf", login(t, "teacher")), 404)
	})
	t.Run("logout-is-client-side-and-stateless", func(t *testing.T) {
		first := login(t, "student")
		second := login(t, "student")
		if first == second {
			t.Fatal("each login must issue its own token")
		}
		w := request("POST", base+"/api/logout", first)
		var obj map[string]bool
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 1 || !obj["ok"] || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("logout response invalid")
		}
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("logout must not touch cookies")
		}
		// Nothing is stored server side, so the token itself stays valid until exp
		// and the other login is unaffected.
		if request("GET", base+"/api/me", first).Code != 200 || request("GET", base+"/api/me", second).Code != 200 {
			t.Fatal("stateless tokens must remain usable")
		}
		var tables int
		if pool.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=? AND table_name='sessions'", cfg.Name).Scan(&tables) != nil || tables != 0 {
			t.Fatal("no replayable session table may exist")
		}
	})
	t.Run("database-outage-and-recovery", func(t *testing.T) {
		token := login(t, "teacher")
		exec(t, "RENAME TABLE users TO session_users_unavailable")
		checkError(t, request("GET", base+"/api/me", token), 503)
		exec(t, "RENAME TABLE session_users_unavailable TO users")
		if request("GET", base+"/api/me", token).Code != 200 {
			t.Fatal("recovered database lost the token identity")
		}
		if request("POST", base+"/api/logout", token).Code != 200 {
			t.Fatal("logout did not recover")
		}
		pool.Close()
		checkError(t, request("GET", base+"/api/me", token), 503)
		checkError(t, request("POST", base+"/api/logout", token), 503)
		if request("GET", base+"/health", "").Code != 200 {
			t.Fatal("health depends on database")
		}
	})
}
