package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
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
	request := func(method, path string, cookie *http.Cookie, token, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if token != "" {
			r.Header.Set("X-CSRF-Token", token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	checkError := func(t *testing.T, w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("got %d want %d", w.Code, status)
		}
		var obj map[string]string
		codes := map[int]string{401: "unauthenticated", 403: "csrf_invalid", 503: "service_unavailable"}
		messages := map[int]string{401: "请先登录", 403: "请求校验失败", 503: "服务暂不可用"}
		if json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 2 || obj["error"] != codes[status] || obj["message"] != messages[status] || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("unsafe failure response")
		}
	}
	csrf := func(t *testing.T, base string, cookie *http.Cookie) (*http.Cookie, string) {
		t.Helper()
		w := request("GET", base+"/api/csrf", cookie, "", "", "")
		if w.Code != 200 {
			t.Fatal("CSRF prerequisite failed")
		}
		if len(w.Result().Cookies()) != 0 {
			cookie = w.Result().Cookies()[0]
		}
		var obj map[string]string
		if json.Unmarshal(w.Body.Bytes(), &obj) != nil {
			t.Fatal("invalid CSRF response")
		}
		return cookie, obj["csrf_token"]
	}
	login := func(t *testing.T, base, role string) (*http.Cookie, string) {
		t.Helper()
		cookie, token := csrf(t, base, nil)
		w := request("POST", base+"/api/login", cookie, token, loginJSON("session-"+role, password), base)
		if w.Code != 200 || len(w.Result().Cookies()) != 1 {
			t.Fatal("login prerequisite failed")
		}
		return csrf(t, base, w.Result().Cookies()[0])
	}
	countSession := func(t *testing.T, cookie *http.Cookie) int {
		t.Helper()
		raw, ok := decodeValue(cookie.Value)
		if !ok {
			t.Fatal("invalid fixture cookie")
		}
		var n int
		if pool.QueryRow("SELECT COUNT(*) FROM sessions WHERE session_id_hash=?", security.digest(raw)).Scan(&n) != nil {
			t.Fatal("cannot count session")
		}
		return n
	}
	const base = "http://localhost:8080"
	t.Run("teacher-student-me-latest-identity", func(t *testing.T) {
		for _, role := range []string{"teacher", "student"} {
			cookie, _ := login(t, base, role)
			w := request("GET", base+"/api/me?role=admin&class_id=999", cookie, "", "", "")
			var obj map[string]json.RawMessage
			var got userIdentity
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 4 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID == 0 || got.Username != "session-"+role || got.Role != role || got.ClassID != uint64(classA) || w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
				t.Fatal("identity contract failed")
			}
			exec(t, "UPDATE users SET class_id=? WHERE id=?", classB, got.ID)
			w = request("GET", base+"/api/me", cookie, "", "", "")
			json.Unmarshal(w.Body.Bytes(), &got)
			if w.Code != 200 || got.ClassID != uint64(classB) {
				t.Fatal("me used stale identity")
			}
			exec(t, "UPDATE users SET class_id=? WHERE id=?", classA, got.ID)
		}
	})
	t.Run("anonymous-invalid-expired-priority", func(t *testing.T) {
		anonymous, token := csrf(t, base, nil)
		_, unknown := randomValue()
		cookies := []*http.Cookie{nil, anonymous, {Name: sessionCookie, Value: "invalid"}, {Name: sessionCookie, Value: unknown}}
		expired, _ := login(t, base, "student")
		raw, _ := decodeValue(expired.Value)
		exec(t, "UPDATE sessions SET created_at=?,expires_at=?,csrf_token_hash=NULL,csrf_expires_at=NULL WHERE session_id_hash=?", time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-time.Hour), security.digest(raw))
		cookies = append(cookies, expired)
		for _, cookie := range cookies {
			checkError(t, request("GET", base+"/api/me", cookie, "", "", ""), 401)
			checkError(t, request("POST", base+"/api/logout", cookie, token, "", "http://evil.test"), 401)
		}
	})
	t.Run("csrf-failures-preserve-session", func(t *testing.T) {
		cookie, token := login(t, base, "teacher")
		for _, bad := range []string{"", "invalid"} {
			checkError(t, request("POST", base+"/api/logout", cookie, bad, "", base), 403)
		}
		for _, origin := range []string{"", "http://evil.test"} {
			checkError(t, request("POST", base+"/api/logout", cookie, token, "", origin), 403)
		}
		cookie, fresh := csrf(t, base, cookie)
		checkError(t, request("POST", base+"/api/logout", cookie, token, "", base), 403)
		if countSession(t, cookie) != 1 || request("GET", base+"/api/me", cookie, "", "", "").Code != 200 {
			t.Fatal("CSRF rejection changed session")
		}
		raw, _ := decodeValue(cookie.Value)
		exec(t, "UPDATE sessions SET csrf_expires_at=? WHERE session_id_hash=?", time.Now().UTC().Add(-time.Second), security.digest(raw))
		checkError(t, request("POST", base+"/api/logout", cookie, fresh, "", base), 403)
	})
	t.Run("logout-clears-cookie-and-rejects-replay", func(t *testing.T) {
		for _, target := range []string{base, "https://localhost:8443"} {
			cookie, token := login(t, target, "student")
			other, _ := login(t, target, "student")
			w := request("POST", target+"/api/logout", cookie, token, "", target)
			var obj map[string]bool
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 1 || !obj["ok"] || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("logout response invalid")
			}
			cleared := w.Result().Cookies()
			if len(cleared) != 1 {
				t.Fatal("missing cookie deletion")
			}
			c := cleared[0]
			if c.Name != sessionCookie || c.Value != "" || c.Path != "/" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != -1 || !c.Expires.Before(time.Now()) || c.Secure != strings.HasPrefix(target, "https:") {
				t.Fatal("cookie deletion attributes invalid")
			}
			if countSession(t, cookie) != 0 || countSession(t, other) != 1 {
				t.Fatal("wrong session deleted")
			}
			checkError(t, request("GET", target+"/api/me", cookie, "", "", ""), 401)
			checkError(t, request("POST", target+"/api/logout", cookie, token, "", target), 401)
			if request("GET", target+"/api/me", other, "", "", "").Code != 200 {
				t.Fatal("other session revoked")
			}
		}
	})
	t.Run("delete-failure-rollback-and-database-outage", func(t *testing.T) {
		cookie, token := login(t, base, "teacher")
		exec(t, "CREATE TRIGGER session_reject_delete BEFORE DELETE ON sessions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='private failure detail'")
		checkError(t, request("POST", base+"/api/logout", cookie, token, "", base), 503)
		exec(t, "DROP TRIGGER session_reject_delete")
		if countSession(t, cookie) != 1 || request("GET", base+"/api/me", cookie, "", "", "").Code != 200 {
			t.Fatal("failed delete revoked session")
		}
		exec(t, "RENAME TABLE users TO session_users_unavailable")
		checkError(t, request("GET", base+"/api/me", cookie, "", "", ""), 503)
		exec(t, "RENAME TABLE session_users_unavailable TO users")
		exec(t, "RENAME TABLE sessions TO sessions_unavailable")
		checkError(t, request("GET", base+"/api/me", cookie, "", "", ""), 503)
		checkError(t, request("POST", base+"/api/logout", cookie, token, "", base), 503)
		exec(t, "RENAME TABLE sessions_unavailable TO sessions")
		if request("GET", base+"/api/me", cookie, "", "", "").Code != 200 {
			t.Fatal("recovered database lost session")
		}
		if request("POST", base+"/api/logout", cookie, token, "", base).Code != 200 {
			t.Fatal("logout did not recover")
		}
		pool.Close()
		checkError(t, request("GET", base+"/api/me", cookie, "", "", ""), 503)
		checkError(t, request("POST", base+"/api/logout", cookie, token, "", base), 503)
		if request("GET", base+"/health", nil, "", "", "").Code != 200 {
			t.Fatal("health depends on database")
		}
	})
}
