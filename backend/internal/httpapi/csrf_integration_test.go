package httpapi

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"golang.org/x/crypto/scrypt"
)

func TestMySQL8CSRF(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("requires isolated MySQL 8.0 fixture; run scripts/verify-schema.ps1 -Suite csrf")
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
	if err = pool.QueryRow("SELECT VERSION()").Scan(&version); err != nil || !strings.HasPrefix(version, "8.0.") {
		t.Fatal("requires live MySQL 8.0")
	}
	t.Log("MySQL", version)
	secret, _ := randomValue()
	s := NewSecurity(pool, string(secret))
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	exec := func(query string, args ...any) sql.Result {
		t.Helper()
		result, err := pool.Exec(query, args...)
		if err != nil {
			t.Fatal("fixture SQL failed")
		}
		return result
	}
	bootstrap := func(cookie *http.Cookie) (*httptest.ResponseRecorder, string) {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost:8080/api/csrf", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.CSRF(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("bootstrap contract failed")
		}
		var obj map[string]string
		if json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 1 {
			t.Fatal("unexpected token response")
		}
		if _, ok := decodeValue(obj["csrf_token"]); !ok {
			t.Fatal("token is not 32-byte Base64URL")
		}
		return w, obj["csrf_token"]
	}
	write := func(cookie *http.Cookie, token string, required bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost:8080/test-only", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if token != "" {
			r.Header.Set("X-CSRF-Token", token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		called := false
		s.ProtectWrite(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }), required).ServeHTTP(w, r)
		if w.Code != 204 && called {
			t.Fatal("rejected request reached test handler")
		}
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d, want %d", w.Code, status)
		}
		if status == 204 {
			return
		}
		var obj map[string]string
		codes := map[int]string{401: "unauthenticated", 403: "csrf_invalid", 503: "service_unavailable"}
		messages := map[int]string{401: "请先登录", 403: "请求校验失败", 503: "服务暂不可用"}
		if json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 2 || obj["error"] != codes[status] || obj["message"] != messages[status] || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unsafe error contract")
		}
	}
	var anonymous *http.Cookie
	var firstToken string
	t.Run("anonymous-cookie-digests-and-no-store", func(t *testing.T) {
		w, token := bootstrap(nil)
		firstToken = token
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatal("expected session cookie")
		}
		anonymous = cookies[0]
		if anonymous.Name != sessionCookie || anonymous.Path != "/" || !anonymous.HttpOnly || anonymous.SameSite != http.SameSiteLaxMode || anonymous.Secure || anonymous.MaxAge != 1800 {
			t.Fatal("cookie attributes invalid")
		}
		raw, ok := decodeValue(anonymous.Value)
		if !ok {
			t.Fatal("invalid opaque session ID")
		}
		var uid sql.NullInt64
		var stored []byte
		var created, expires, csrfExpires time.Time
		err = pool.QueryRow("SELECT user_id, csrf_token_hash, created_at, expires_at, csrf_expires_at FROM sessions WHERE session_id_hash=?", s.digest(raw)).Scan(&uid, &stored, &created, &expires, &csrfExpires)
		tokenRaw, _ := decodeValue(token)
		digest := sha256.Sum256(tokenRaw)
		if err != nil || uid.Valid || !bytes.Equal(stored, digest[:]) || !created.Equal(now) || !expires.Equal(now.Add(30*time.Minute)) || !csrfExpires.Equal(expires) {
			t.Fatal("stored session/token contract invalid")
		}
		check(write(anonymous, token, false, "http://localhost:8080"), 204)
		check(write(anonymous, token, true, "http://localhost:8080"), 401)
	})
	if anonymous == nil {
		t.Fatal("bootstrap prerequisite failed")
	}
	t.Run("rotation-boundaries-and-invalid-input", func(t *testing.T) {
		now = now.Add(time.Minute)
		w, token := bootstrap(anonymous)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("existing session cookie replaced")
		}
		check(write(anonymous, firstToken, false, "http://localhost:8080"), 403)
		check(write(anonymous, token, false, "http://localhost:8080"), 204)
		for _, bad := range []string{"", "invalid", strings.Repeat("A", 43)} {
			check(write(anonymous, bad, false, "http://localhost:8080"), 403)
		}
		for _, badOrigin := range []string{"", "null", "http://localhost:9999", "https://localhost:8080", "http://evil.test"} {
			check(write(anonymous, token, false, badOrigin), 403)
		}
		check(write(nil, token, true, ""), 401)
		check(write(&http.Cookie{Name: sessionCookie, Value: "invalid"}, token, true, ""), 401)
		_, otherToken := bootstrap(nil)
		check(write(anonymous, otherToken, false, "http://localhost:8080"), 403)
		now = time.Date(2026, 9, 26, 10, 30, 0, 0, time.UTC)
		check(write(anonymous, token, false, "http://localhost:8080"), 403)
		check(write(anonymous, token, true, "http://localhost:8080"), 401)
		renewed, _ := bootstrap(anonymous)
		if len(renewed.Result().Cookies()) != 1 || renewed.Result().Cookies()[0].Value == anonymous.Value {
			t.Fatal("expired session reused")
		}
	})
	// Independent real user fixture, not product seeds or a login implementation.
	classResult := exec("INSERT INTO classes (name) VALUES (?)", "csrf-test-class")
	classID, _ := classResult.LastInsertId()
	password := make([]byte, 32)
	salt := make([]byte, 16)
	rand.Read(password)
	rand.Read(salt)
	key, err := scrypt.Key(password, salt, 32768, 8, 1, 32)
	if err != nil {
		t.Fatal("fixture hashing failed")
	}
	passwordHash := fmt.Sprintf("$scrypt$32768$8$1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	userResult := exec("INSERT INTO users (username,password_hash,role,class_id) VALUES (?,?,?,?)", "csrf-test-user", passwordHash, "student", classID)
	userID, _ := userResult.LastInsertId()
	newLogged := func(lifetime time.Duration) *http.Cookie {
		raw, id := randomValue()
		exec("INSERT INTO sessions (session_id_hash,user_id,created_at,expires_at) VALUES (?,?,?,?)", s.digest(raw), userID, now, now.Add(lifetime))
		return &http.Cookie{Name: sessionCookie, Value: id}
	}
	t.Run("logged-in-token-expiry-and-session-cap", func(t *testing.T) {
		logged := newLogged(8 * time.Hour)
		w, token := bootstrap(logged)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("logged-in session replaced")
		}
		check(write(logged, token, true, "http://localhost:8080"), 204)
		now = now.Add(30*time.Minute - time.Microsecond)
		check(write(logged, token, true, "http://localhost:8080"), 204)
		now = now.Add(time.Microsecond)
		check(write(logged, token, true, "http://localhost:8080"), 403)
		_, fresh := bootstrap(logged)
		check(write(logged, fresh, true, "http://localhost:8080"), 204)
		short := newLogged(time.Minute)
		_, shortToken := bootstrap(short)
		raw, _ := decodeValue(short.Value)
		var a, b time.Time
		if err := pool.QueryRow("SELECT expires_at,csrf_expires_at FROM sessions WHERE session_id_hash=?", s.digest(raw)).Scan(&a, &b); err != nil || !a.Equal(b) {
			t.Fatal("token outlives session")
		}
		now = now.Add(time.Minute)
		check(write(short, shortToken, true, "http://localhost:8080"), 401)
		check(write(short, shortToken, false, "http://localhost:8080"), 403)
		raw, _ = decodeValue(logged.Value)
		exec("DELETE FROM sessions WHERE session_id_hash=?", s.digest(raw))
		check(write(logged, fresh, true, "http://localhost:8080"), 401)
	})
	t.Run("https-cookie-and-production-route", func(t *testing.T) {
		w := httptest.NewRecorder()
		NewHandler(pool, string(secret)).ServeHTTP(w, httptest.NewRequest("GET", "https://localhost/api/csrf", nil))
		if w.Code != 200 || len(w.Result().Cookies()) != 1 || !w.Result().Cookies()[0].Secure {
			t.Fatal("HTTPS cookie/route failed")
		}
	})
	t.Run("database-read-and-write-failures", func(t *testing.T) {
		logged := newLogged(time.Hour)
		_, token := bootstrap(logged)
		// Test-only triggers inject write failures while reads and transaction start work.
		exec("CREATE TRIGGER csrf_test_reject_insert BEFORE INSERT ON sessions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'test database failure'")
		w := httptest.NewRecorder()
		s.CSRF(w, httptest.NewRequest("GET", "http://localhost:8080/api/csrf", nil))
		check(w, 503)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("cookie issued for failed insert")
		}
		exec("DROP TRIGGER csrf_test_reject_insert")
		exec("CREATE TRIGGER csrf_test_reject_update BEFORE UPDATE ON sessions FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'test database failure'")
		r := httptest.NewRequest("GET", "http://localhost:8080/api/csrf", nil)
		r.AddCookie(logged)
		w = httptest.NewRecorder()
		s.CSRF(w, r)
		check(w, 503)
		exec("DROP TRIGGER csrf_test_reject_update")
		check(write(logged, token, true, "http://localhost:8080"), 204)
		pool.Close()
		check(write(logged, token, true, "http://localhost:8080"), 503)
		w = httptest.NewRecorder()
		s.CSRF(w, r)
		check(w, 503)
		w = httptest.NewRecorder()
		NewHandler(pool, string(secret)).ServeHTTP(w, httptest.NewRequest("GET", "http://localhost:8080/health", nil))
		if w.Code != 200 {
			t.Fatal("health depends on database")
		}
	})
}
