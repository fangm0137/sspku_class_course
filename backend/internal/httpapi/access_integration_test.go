package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/fixtures"
	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/seed"
)

type forbiddenBody struct{ t *testing.T }

func (b forbiddenBody) Read([]byte) (int, error) {
	b.t.Fatal("rejected request body read")
	return 0, nil
}
func (b forbiddenBody) Close() error { return nil }

func TestMySQL8Access(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run verify-schema.ps1 -Suite access")
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
	_, password := randomValue()
	_, secret := randomValue()
	passwords, _ := seed.LoadPasswords(func(string) string { return password })
	if _, err := seed.Apply(context.Background(), pool, passwords); err != nil {
		t.Fatal("seed accounts failed")
	}
	if _, err := seed.ApplyMaterial(context.Background(), pool, directory); err != nil {
		t.Fatal("B seed failed")
	}
	exec := func(t *testing.T, q string, args ...any) sql.Result {
		t.Helper()
		res, err := pool.Exec(q, args...)
		if err != nil {
			t.Fatal("fixture SQL failed")
		}
		return res
	}
	var a, b, teacher uint64
	if pool.QueryRow("SELECT id FROM classes WHERE name='A'").Scan(&a) != nil || pool.QueryRow("SELECT id FROM classes WHERE name='B'").Scan(&b) != nil || pool.QueryRow("SELECT id FROM users WHERE username='teacher_a'").Scan(&teacher) != nil {
		t.Fatal("fixture identity lookup failed")
	}
	contents := map[uint64][]byte{}
	classes := map[uint64]uint64{}
	keys := map[uint64]string{}
	for _, name := range []string{"class-a-intro.txt", "class-a-notes.md"} {
		content, _ := fixtures.Files.ReadFile(name)
		key := "test-" + name
		result := exec(t, "INSERT INTO materials (class_id,uploader_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?,?)", a, teacher, name, key, len(content), "upload")
		id, _ := result.LastInsertId()
		exec(t, "INSERT INTO knowledge_entries (material_id,class_id,extracted_text) VALUES (?,?,?)", id, a, string(content))
		if os.WriteFile(filepath.Join(directory, key), content, 0600) != nil {
			t.Fatal("fixture file failed")
		}
		contents[uint64(id)] = content
		classes[uint64(id)] = a
		keys[uint64(id)] = key
	}
	var bid uint64
	if pool.QueryRow("SELECT id FROM materials WHERE storage_key=?", seed.MaterialKey).Scan(&bid) != nil {
		t.Fatal("B material missing")
	}
	contents[bid], _ = fixtures.Files.ReadFile("class-b-seed.txt")
	classes[bid] = b
	keys[bid] = seed.MaterialKey
	handler := NewHandler(pool, secret, directory)
	security := NewSecurity(pool, secret)
	request := func(target string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost"+target, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	login := func(username string) *http.Cookie {
		t.Helper()
		w := request("/api/csrf", nil)
		if w.Code != 200 {
			t.Fatal("csrf failed")
		}
		var token map[string]string
		json.Unmarshal(w.Body.Bytes(), &token)
		r := httptest.NewRequest("POST", "http://localhost/api/login", strings.NewReader(loginJSON(username, password)))
		r.AddCookie(w.Result().Cookies()[0])
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("X-CSRF-Token", token["csrf_token"])
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("login failed")
		}
		return w.Result().Cookies()[0]
	}
	cookies := map[string]*http.Cookie{}
	for _, name := range []string{"teacher_a", "student_a1", "student_b1"} {
		cookies[name] = login(name)
	}
	checkError := func(t *testing.T, w *httptest.ResponseRecorder, status int, code, message string) {
		t.Helper()
		var obj map[string]string
		if w.Code != status || json.Unmarshal(w.Body.Bytes(), &obj) != nil || len(obj) != 2 || obj["error"] != code || obj["message"] != message || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("response contract failed, status=%d expected=%d", w.Code, status)
		}
	}
	gateRequest := func(cookie *http.Cookie, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/test-upload?role="+role+"&class_id=999", nil)
		r.Body = forbiddenBody{t}
		r.Header.Set("X-Role", role)
		r.Header.Set("X-Class-ID", "999")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		security.RequireIdentity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, r.Context().Value(identityKey{})) }), true).ServeHTTP(w, r)
		return w
	}
	t.Run("T4.1-trusted-identity-role-and-no-side-effects", func(t *testing.T) {
		for _, name := range []string{"student_a1", "student_b1"} {
			checkError(t, gateRequest(cookies[name], "teacher"), 403, "forbidden", "无权执行此操作")
		}
		checkError(t, gateRequest(nil, "teacher"), 401, "unauthenticated", "请先登录")
		w := gateRequest(cookies["teacher_a"], "student")
		var got userIdentity
		json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != 200 || got.ID != teacher || got.ClassID != a || got.Role != "teacher" {
			t.Fatal("untrusted request changed identity")
		}
		exec(t, "UPDATE users SET role='student' WHERE id=?", teacher)
		checkError(t, gateRequest(cookies["teacher_a"], "teacher"), 403, "forbidden", "无权执行此操作")
		exec(t, "UPDATE users SET role='teacher' WHERE id=?", teacher)
		var count int
		pool.QueryRow("SELECT COUNT(*) FROM materials").Scan(&count)
		files, _ := os.ReadDir(directory)
		if count != 3 || len(files) != 3 {
			t.Fatal("authorization changed storage")
		}
		pool.QueryRow("SELECT COUNT(*) FROM knowledge_entries").Scan(&count)
		if count != 3 {
			t.Fatal("authorization changed knowledge")
		}
	})
	t.Run("T4.2-list-detail-file-and-response-fields", func(t *testing.T) {
		for name, cookie := range cookies {
			own := a
			expected := 2
			if name == "student_b1" {
				own = b
				expected = 1
			}
			w := request("/api/materials?class_id=999&role=teacher", cookie)
			var root map[string]json.RawMessage
			var items []map[string]json.RawMessage
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &root) != nil || len(root) != 1 || json.Unmarshal(root["items"], &items) != nil || len(items) != expected {
				t.Fatal("list fields/count invalid")
			}
			for _, item := range items {
				if len(item) != 4 {
					t.Fatal("list leaks internal fields")
				}
				var classID uint64
				var stamp string
				json.Unmarshal(item["class_id"], &classID)
				json.Unmarshal(item["created_at"], &stamp)
				if _, err := time.Parse(time.RFC3339, stamp); err != nil || classID != own {
					t.Fatal("wrong class/timestamp")
				}
			}
			for id, content := range contents {
				if classes[id] != own {
					continue
				}
				target := fmt.Sprintf("/api/materials/%d", id)
				w = request(target, cookie)
				var detail map[string]json.RawMessage
				var text string
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || len(detail) != 5 {
					t.Fatal("detail fields invalid")
				}
				json.Unmarshal(detail["text"], &text)
				if text != string(content) {
					t.Fatal("detail text mismatch")
				}
				w = request(target+"/file", cookie)
				kind, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
				if w.Code != 200 || w.Body.String() != string(content) || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Cache-Control") != "no-store" || err != nil || kind != "attachment" || params["filename"] == "" {
					t.Fatal("download contract failed")
				}
			}
		}
		empty, _ := exec(t, "INSERT INTO classes (name) VALUES (?)", "empty-test").LastInsertId()
		exec(t, "UPDATE users SET class_id=? WHERE username='student_b1'", empty)
		w := request("/api/materials", cookies["student_b1"])
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
			t.Fatal("empty items not array")
		}
		exec(t, "UPDATE users SET class_id=? WHERE username='student_b1'", b)
	})
	t.Run("T4.3-bidirectional-IDs-anonymous-and-bypass", func(t *testing.T) {
		for name, cookie := range cookies {
			own := a
			if name == "student_b1" {
				own = b
			}
			for id := range contents {
				for _, suffix := range []string{"", "/file"} {
					target := fmt.Sprintf("/api/materials/%d%s?class_id=%d&role=teacher", id, suffix, classes[id])
					checkError(t, request(target, nil), 401, "unauthenticated", "请先登录")
					if classes[id] != own {
						cross := request(target, cookie)
						missing := request("/api/materials/99999999"+suffix, cookie)
						checkError(t, cross, 404, "not_found", "资源不存在")
						if cross.Body.String() != missing.Body.String() || missing.Code != 404 {
							t.Fatal("cross-class existence disclosed")
						}
					}
				}
			}
			for _, id := range []string{"abc", "0", "01", "-1", "18446744073709551616", "%2e%2e%2fsecret"} {
				checkError(t, request("/api/materials/"+id, cookie), 404, "not_found", "资源不存在")
				checkError(t, request("/api/materials/"+id, nil), 401, "unauthenticated", "请先登录")
			}
		}
		checkError(t, request("/api/materials", nil), 401, "unauthenticated", "请先登录")
		for _, target := range []string{"/uploads/" + seed.MaterialKey, "/data/uploads/" + seed.MaterialKey, "/static/" + seed.MaterialKey, "/api/knowledge_entries/1", "/knowledge/1", "/fixtures/class-b-seed.txt"} {
			for _, cookie := range []*http.Cookie{nil, cookies["teacher_a"], cookies["student_b1"]} {
				checkError(t, request(target, cookie), 404, "not_found", "资源不存在")
			}
		}
		for _, cookie := range []*http.Cookie{{Name: sessionCookie, Value: "invalid"}, func() *http.Cookie { _, id := randomValue(); return &http.Cookie{Name: sessionCookie, Value: id} }()} {
			checkError(t, request("/api/materials", cookie), 401, "unauthenticated", "请先登录")
		}
	})
	t.Run("T4.2-missing-content-and-storage-containment", func(t *testing.T) {
		cookie := cookies["student_b1"]
		target := fmt.Sprintf("/api/materials/%d", bid)
		exec(t, "DELETE FROM knowledge_entries WHERE material_id=?", bid)
		checkError(t, request(target, cookie), 500, "internal_error", "服务内部错误")
		exec(t, "INSERT INTO knowledge_entries (material_id,class_id,extracted_text) VALUES (?,?,?)", bid, b, string(contents[bid]))
		if os.Remove(filepath.Join(directory, keys[bid])) != nil {
			t.Fatal("fault setup failed")
		}
		checkError(t, request(target+"/file", cookie), 500, "internal_error", "服务内部错误")
		checkError(t, request(target+"/file", cookies["teacher_a"]), 404, "not_found", "资源不存在")
		exec(t, "UPDATE materials SET storage_key=? WHERE id=?", "../private.txt", bid)
		checkError(t, request(target+"/file", cookie), 500, "internal_error", "服务内部错误")
		exec(t, "UPDATE materials SET storage_key=? WHERE id=?", keys[bid], bid)
		if os.WriteFile(filepath.Join(directory, keys[bid]), contents[bid], 0600) != nil {
			t.Fatal("fixture restore failed")
		}
	})
	t.Run("T4.1-T4.2-database-failure-is-503", func(t *testing.T) {
		exec(t, "RENAME TABLE materials TO materials_unavailable")
		for _, target := range []string{"/api/materials", fmt.Sprintf("/api/materials/%d", bid), fmt.Sprintf("/api/materials/%d/file", bid)} {
			checkError(t, request(target, cookies["student_b1"]), 503, "service_unavailable", "服务暂不可用")
		}
		exec(t, "RENAME TABLE materials_unavailable TO materials")
		pool.Close()
		checkError(t, gateRequest(cookies["teacher_a"], "teacher"), 503, "service_unavailable", "服务暂不可用")
		for _, target := range []string{"/api/materials", fmt.Sprintf("/api/materials/%d", bid), fmt.Sprintf("/api/materials/%d/file", bid)} {
			checkError(t, request(target, cookies["student_b1"]), 503, "service_unavailable", "服务暂不可用")
		}
		if request("/health", nil).Code != 200 {
			t.Fatal("health depends on DB")
		}
	})
}
