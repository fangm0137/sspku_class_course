package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
)

func TestMySQL8Login(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run isolated verify-schema.ps1 -Suite login")
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
	classA, _ := exec(t, "INSERT INTO classes (name) VALUES (?)", "login-test-A").LastInsertId()
	classB, _ := exec(t, "INSERT INTO classes (name) VALUES (?)", "login-test-B").LastInsertId()
	_, randomPassword := randomValue()
	fixtures := []struct {
		name, password, role string
		classID              int64
		id                   int64
	}{
		{strings.Repeat("教", 64), strings.Repeat(randomPassword[:1], 128), "teacher", classA, 0},
		{"学", randomPassword[:1], "student", classA, 0},
		{"login-student-b", " " + randomPassword + " ", "student", classB, 0},
	}
	for i := range fixtures {
		f := &fixtures[i]
		f.id, _ = exec(t, "INSERT INTO users (username,password_hash,role,class_id) VALUES (?,?,?,?)", f.name, fixturePasswordHash(t, f.password), f.role, f.classID).LastInsertId()
	}
	bootstrap := func(t *testing.T, cookie *http.Cookie) (*http.Cookie, string) {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost:8080/api/csrf", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("CSRF prerequisite failed")
		}
		if len(w.Result().Cookies()) > 0 {
			cookie = w.Result().Cookies()[0]
		}
		var obj map[string]string
		if json.Unmarshal(w.Body.Bytes(), &obj) != nil {
			t.Fatal("CSRF response invalid")
		}
		return cookie, obj["csrf_token"]
	}
	post := func(cookie *http.Cookie, token, body, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost:8080/api/login", strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("Origin", "http://localhost:8080")
		r.Header.Set("X-CSRF-Token", token)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	checkError := func(t *testing.T, w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("got status %d want %d", w.Code, status)
		}
		var obj map[string]string
		codes := map[int]string{400: "invalid_request", 401: "invalid_credentials", 403: "csrf_invalid", 503: "service_unavailable"}
		messages := map[int]string{400: "请求无效", 401: "账号或密码错误", 403: "请求校验失败", 503: "服务暂不可用"}
		if json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 2 || obj["error"] != codes[status] || obj["message"] != messages[status] || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unsafe failure response")
		}
	}
	countLogged := func(t *testing.T) int {
		t.Helper()
		var n int
		if pool.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id IS NOT NULL").Scan(&n) != nil {
			t.Fatal("count failed")
		}
		return n
	}
	for _, f := range fixtures {
		t.Run("success-"+f.role+"-"+string(rune('0'+f.id)), func(t *testing.T) {
			old, token := bootstrap(t, nil)
			before := time.Now().UTC()
			w := post(old, token, loginJSON(f.name, f.password), "application/json")
			if w.Code != 200 {
				t.Fatalf("login failed: %d", w.Code)
			}
			var root map[string]json.RawMessage
			var user map[string]json.RawMessage
			if json.Unmarshal(w.Body.Bytes(), &root) != nil || len(root) != 1 || json.Unmarshal(root["user"], &user) != nil || len(user) != 4 {
				t.Fatal("wrong user response shape")
			}
			var got userIdentity
			json.Unmarshal(root["user"], &got)
			if got.ID != uint64(f.id) || got.Username != f.name || got.Role != f.role || got.ClassID != uint64(f.classID) {
				t.Fatal("identity mismatch")
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatal("missing session cookie")
			}
			current := cookies[0]
			if current.Value == old.Value || current.MaxAge != 28800 || !current.HttpOnly || current.SameSite != http.SameSiteLaxMode || current.Path != "/" || current.Secure {
				t.Fatal("cookie contract failed")
			}
			raw, ok := decodeValue(current.Value)
			if !ok {
				t.Fatal("cookie is not opaque 32-byte ID")
			}
			var created, expires time.Time
			var csrf []byte
			var uid uint64
			if pool.QueryRow("SELECT user_id,created_at,expires_at,csrf_token_hash FROM sessions WHERE session_id_hash=?", security.digest(raw)).Scan(&uid, &created, &expires, &csrf) != nil || uid != uint64(f.id) || expires.Sub(created) != 8*time.Hour || created.Before(before.Add(-time.Second)) || csrf != nil {
				t.Fatal("stored session invalid")
			}
			oldRaw, _ := decodeValue(old.Value)
			var n int
			pool.QueryRow("SELECT COUNT(*) FROM sessions WHERE session_id_hash=?", security.digest(oldRaw)).Scan(&n)
			if n != 0 {
				t.Fatal("old session retained")
			}
			checkError(t, post(old, token, loginJSON(f.name, f.password), "application/json"), 403)
			checkError(t, post(current, token, loginJSON(f.name, f.password), "application/json"), 403)
			// Existing logged-in session also rotates, after obtaining its new CSRF token.
			current, newToken := bootstrap(t, current)
			second := post(current, newToken, loginJSON(f.name, f.password), "application/json")
			if second.Code != 200 || second.Result().Cookies()[0].Value == current.Value {
				t.Fatal("logged-in replacement failed")
			}
			checkError(t, post(current, newToken, loginJSON(f.name, f.password), "application/json"), 403)
		})
	}
	t.Run("malformed-input-and-priority", func(t *testing.T) {
		cookie, token := bootstrap(t, nil)
		before := countLogged(t)
		for name, body := range malformedLogins() {
			t.Run(name, func(t *testing.T) { checkError(t, post(cookie, token, body, "application/json"), 400) })
		}
		checkError(t, post(cookie, token, loginJSON("x", "x"), "text/plain"), 400)
		checkError(t, post(cookie, "wrong", "{", "application/json"), 403)
		if countLogged(t) != before {
			t.Fatal("invalid inputs created sessions")
		}
	})
	t.Run("credentials-and-untrusted-fields", func(t *testing.T) {
		cookie, token := bootstrap(t, nil)
		before := countLogged(t)
		a := post(cookie, token, loginJSON("missing-account", randomPassword), "application/json")
		b := post(cookie, token, loginJSON(fixtures[0].name, randomPassword+"wrong"), "application/json")
		checkError(t, a, 401)
		checkError(t, b, 401)
		if a.Body.String() != b.Body.String() {
			t.Fatal("account existence leaked")
		}
		checkError(t, post(cookie, token, loginJSON(fixtures[2].name, strings.TrimSpace(fixtures[2].password)), "application/json"), 401)
		if countLogged(t) != before {
			t.Fatal("bad credentials created sessions")
		}
		body, _ := json.Marshal(map[string]any{"username": fixtures[1].name, "password": fixtures[1].password, "role": "teacher", "class_id": classB})
		w := post(cookie, token, string(body), "application/json")
		var response struct {
			User userIdentity `json:"user"`
		}
		json.Unmarshal(w.Body.Bytes(), &response)
		if w.Code != 200 || response.User.Role != "student" || response.User.ClassID != uint64(classA) {
			t.Fatal("client controlled identity")
		}
	})
	t.Run("concurrent-session-replacement", func(t *testing.T) {
		cookie, token := bootstrap(t, nil)
		before := countLogged(t)
		var wg sync.WaitGroup
		results := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- post(cookie, token, loginJSON(fixtures[1].name, fixtures[1].password), "application/json").Code
			}()
		}
		wg.Wait()
		close(results)
		counts := map[int]int{}
		for code := range results {
			counts[code]++
		}
		if counts[200] != 1 || counts[403] != 1 || countLogged(t) != before+1 {
			t.Fatal("old session consumed more than once")
		}
	})
	t.Run("session-expiry-eight-hours", func(t *testing.T) {
		cookie, token := bootstrap(t, nil)
		w := post(cookie, token, loginJSON(fixtures[1].name, fixtures[1].password), "application/json")
		if w.Code != 200 {
			t.Fatal("login failed")
		}
		cookie = w.Result().Cookies()[0]
		raw, _ := decodeValue(cookie.Value)
		var expires time.Time
		if pool.QueryRow("SELECT expires_at FROM sessions WHERE session_id_hash=?", security.digest(raw)).Scan(&expires) != nil {
			t.Fatal("session query failed")
		}
		// Force a freshly issued token up to the session deadline to isolate identity expiry.
		tokenRaw, encoded := randomValue()
		digest := sha256.Sum256(tokenRaw)
		exec(t, "UPDATE sessions SET csrf_token_hash=?,csrf_expires_at=expires_at WHERE session_id_hash=?", digest[:], security.digest(raw))
		gate := security.ProtectWrite(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), true)
		for _, tc := range []struct {
			now    time.Time
			status int
		}{{expires.Add(-time.Microsecond), 204}, {expires, 401}} {
			security.now = func() time.Time { return tc.now }
			r := httptest.NewRequest("POST", "http://localhost:8080/test-only", nil)
			r.AddCookie(cookie)
			r.Header.Set("Origin", "http://localhost:8080")
			r.Header.Set("X-CSRF-Token", encoded)
			out := httptest.NewRecorder()
			gate.ServeHTTP(out, r)
			if out.Code != tc.status {
				t.Fatal("8-hour boundary incorrect")
			}
		}
		security.now = time.Now
	})
	t.Run("database-failures-and-rollback", func(t *testing.T) {
		cookie, token := bootstrap(t, nil)
		before := countLogged(t)
		body := loginJSON(fixtures[1].name, fixtures[1].password)
		exec(t, "CREATE TRIGGER login_reject_insert BEFORE INSERT ON sessions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'")
		checkError(t, post(cookie, token, body, "application/json"), 503)
		exec(t, "DROP TRIGGER login_reject_insert")
		if countLogged(t) != before {
			t.Fatal("failed insert partially committed")
		}
		raw, _ := decodeValue(cookie.Value)
		var n int
		pool.QueryRow("SELECT COUNT(*) FROM sessions WHERE session_id_hash=?", security.digest(raw)).Scan(&n)
		if n != 1 {
			t.Fatal("rollback lost old session")
		}
		exec(t, "CREATE TRIGGER login_reject_delete BEFORE DELETE ON sessions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'")
		checkError(t, post(cookie, token, body, "application/json"), 503)
		exec(t, "DROP TRIGGER login_reject_delete")
		// Break user lookup without breaking the CSRF table to test login query errors.
		exec(t, "RENAME TABLE users TO users_test_unavailable")
		checkError(t, post(cookie, token, body, "application/json"), 503)
		exec(t, "RENAME TABLE users_test_unavailable TO users")
		if post(cookie, token, body, "application/json").Code != 200 {
			t.Fatal("failed login did not preserve reusable old session")
		}
		pool.Close()
		checkError(t, post(cookie, token, body, "application/json"), 503)
	})
}
