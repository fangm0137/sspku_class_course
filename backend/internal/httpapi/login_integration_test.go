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
	// Login accepts a plain JSON envelope: there is no cookie bound CSRF token.
	post := func(body, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost:8080/api/login", strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	checkError := func(t *testing.T, w *httptest.ResponseRecorder, status int) {
		t.Helper()
		var obj map[string]string
		codes := map[int]string{400: "invalid_request", 401: "invalid_credentials", 503: "service_unavailable"}
		messages := map[int]string{400: "请求无效", 401: "账号或密码错误", 503: "服务暂不可用"}
		if w.Code != status || json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 2 || obj["error"] != codes[status] || obj["message"] != messages[status] || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unsafe failure response")
		}
	}
	type issued struct {
		User      userIdentity
		Token     string
		ExpiresAt time.Time
	}
	// decodeLogin asserts the response is exactly user + token + expires_at and
	// that no session cookie is handed out.
	decodeLogin := func(t *testing.T, w *httptest.ResponseRecorder) issued {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("login failed: %d", w.Code)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("login must not set a session cookie")
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &root) != nil || len(root) != 3 {
			t.Fatal("wrong login response shape")
		}
		var out issued
		if json.Unmarshal(root["user"], &out.User) != nil || json.Unmarshal(root["token"], &out.Token) != nil {
			t.Fatal("login response fields invalid")
		}
		var stamp string
		if json.Unmarshal(root["expires_at"], &stamp) != nil {
			t.Fatal("expires_at missing")
		}
		parsed, err := time.Parse(time.RFC3339, stamp)
		if err != nil || out.Token == "" || len(strings.Split(out.Token, ".")) != 3 {
			t.Fatal("token is not a compact JWS with an expiry")
		}
		out.ExpiresAt = parsed
		return out
	}
	for _, f := range fixtures {
		t.Run("success-"+f.role+"-"+string(rune('0'+f.id)), func(t *testing.T) {
			before := time.Now().UTC()
			result := decodeLogin(t, post(loginJSON(f.name, f.password), "application/json"))
			if result.User.ID != uint64(f.id) || result.User.Username != f.name || result.User.Role != f.role || result.User.ClassID != uint64(f.classID) {
				t.Fatal("identity mismatch")
			}
			claims, ok := security.verifyToken(result.Token, before)
			if !ok || claims.Subject != uint64(f.id) || claims.ClassID != uint64(f.classID) || claims.Role != f.role {
				t.Fatal("issued token is not verifiable")
			}
			if lifetime := result.ExpiresAt.Sub(before); lifetime < 8*time.Hour-time.Minute || lifetime > 8*time.Hour+time.Minute {
				t.Fatal("token lifetime is not 8 hours")
			}
			var n int
			if pool.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=? AND table_name='sessions'", cfg.Name).Scan(&n) != nil || n != 0 {
				t.Fatal("stateless login must not keep a session table")
			}
		})
	}
	t.Run("malformed-input", func(t *testing.T) {
		for name, body := range malformedLogins() {
			t.Run(name, func(t *testing.T) { checkError(t, post(body, "application/json"), 400) })
		}
		checkError(t, post(loginJSON("x", "x"), "text/plain"), 400)
	})
	t.Run("credentials-and-untrusted-fields", func(t *testing.T) {
		a := post(loginJSON("missing-account", randomPassword), "application/json")
		b := post(loginJSON(fixtures[0].name, randomPassword+"wrong"), "application/json")
		checkError(t, a, 401)
		checkError(t, b, 401)
		if a.Body.String() != b.Body.String() {
			t.Fatal("account existence leaked")
		}
		checkError(t, post(loginJSON(fixtures[2].name, strings.TrimSpace(fixtures[2].password)), "application/json"), 401)
		body, _ := json.Marshal(map[string]any{"username": fixtures[1].name, "password": fixtures[1].password, "role": "teacher", "class_id": classB})
		result := decodeLogin(t, post(string(body), "application/json"))
		if result.User.Role != "student" || result.User.ClassID != uint64(classA) {
			t.Fatal("client controlled identity")
		}
	})
	t.Run("token-expiry-tampering-and-no-replay-state", func(t *testing.T) {
		result := decodeLogin(t, post(loginJSON(fixtures[1].name, fixtures[1].password), "application/json"))
		gate := security.RequireIdentity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), false)
		call := func(token string) int {
			r := httptest.NewRequest("GET", "http://localhost:8080/test-only", nil)
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			out := httptest.NewRecorder()
			gate.ServeHTTP(out, r)
			return out.Code
		}
		for _, tc := range []struct {
			now    time.Time
			status int
		}{{result.ExpiresAt.Add(-time.Second), 204}, {result.ExpiresAt, 401}, {result.ExpiresAt.Add(time.Hour), 401}} {
			security.now = func() time.Time { return tc.now }
			if got := call(result.Token); got != tc.status {
				t.Fatalf("8-hour boundary incorrect: got %d want %d", got, tc.status)
			}
		}
		security.now = time.Now
		segments := strings.Split(result.Token, ".")
		forged := segments[0] + "." + encodeSegment([]byte(`{"sub":1,"role":"teacher","classId":999,"exp":4102444800}`)) + "." + segments[2]
		unsigned := encodeSegment([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + segments[1] + "."
		for _, bad := range []string{forged, unsigned, segments[0] + "." + segments[1] + ".AAAA", "not-a-token", ""} {
			if got := call(bad); got != 401 {
				t.Fatalf("forged or missing token accepted: %d", got)
			}
		}
		// Logout needs no server side deletion: the token stays valid until it expires.
		second := decodeLogin(t, post(loginJSON(fixtures[1].name, fixtures[1].password), "application/json"))
		if second.Token == result.Token {
			t.Fatal("a second login must still issue its own token")
		}
		if call(result.Token) != 204 {
			t.Fatal("bearer tokens are stateless; the first token must stay usable")
		}
	})
	t.Run("database-failures", func(t *testing.T) {
		body := loginJSON(fixtures[1].name, fixtures[1].password)
		exec(t, "RENAME TABLE users TO users_test_unavailable")
		checkError(t, post(body, "application/json"), 503)
		exec(t, "RENAME TABLE users_test_unavailable TO users")
		if post(body, "application/json").Code != 200 {
			t.Fatal("login did not recover after the table returned")
		}
		pool.Close()
		checkError(t, post(body, "application/json"), 503)
	})
}
