package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

type lostResponse struct{ header http.Header }

func (w *lostResponse) Header() http.Header       { return w.header }
func (w *lostResponse) WriteHeader(int)           {}
func (w *lostResponse) Write([]byte) (int, error) { return 0, errors.New("client disconnected") }

func TestMySQL8Upload(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run verify-schema.ps1 -Suite upload")
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
	_, password := randomValue()
	_, secret := randomValue()
	passwords, err := seed.LoadPasswords(func(string) string { return password })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Apply(context.Background(), pool, passwords); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "uploads")
	if _, err = seed.ApplyMaterial(context.Background(), pool, directory); err != nil {
		t.Fatal(err)
	}
	store := newUploadStore(pool, directory)
	if err = store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(pool, secret, store)
	get := func(target string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost"+target, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	csrf := func(cookie *http.Cookie) (*http.Cookie, string) {
		w := get("/api/csrf", cookie)
		var body map[string]string
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal("csrf failed")
		}
		if cookie == nil {
			cookie = w.Result().Cookies()[0]
		}
		return cookie, body["csrf_token"]
	}
	cookies := map[string]*http.Cookie{}
	tokens := map[string]string{}
	for _, name := range []string{"teacher_a", "student_a1", "student_b1"} {
		cookie, token := csrf(nil)
		r := httptest.NewRequest("POST", "http://localhost/api/login", strings.NewReader(loginJSON(name, password)))
		r.AddCookie(cookie)
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("X-CSRF-Token", token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("login failed")
		}
		cookies[name], tokens[name] = csrf(w.Result().Cookies()[0])
	}
	auth := func(r *http.Request, name string) {
		if name != "" {
			r.AddCookie(cookies[name])
			r.Header.Set("X-CSRF-Token", tokens[name])
		}
		r.Header.Set("Origin", "http://localhost")
	}
	upload := func(name, filename string, body []byte) *httptest.ResponseRecorder {
		r := uploadRequest(testUploadPart{"file", filename, body})
		auth(r, name)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	checkError := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		var body map[string]string
		expected := map[int][2]string{400: {"invalid_request", "请求无效"}, 401: {"unauthenticated", "请先登录"}, 403: {"forbidden", "无权执行此操作"}, 413: {"payload_too_large", "文件超过大小限制"}, 500: {"internal_error", "服务内部错误"}, 503: {"service_unavailable", "服务暂不可用"}}
		if w.Code != status || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body) != 2 || body["error"] != expected[status][0] || body["message"] != expected[status][1] {
			t.Fatalf("safe response mismatch: status=%d expected=%d", w.Code, status)
		}
	}
	counts := func() [4]int {
		t.Helper()
		var got [4]int
		if pool.QueryRow("SELECT COUNT(*) FROM materials").Scan(&got[0]) != nil || pool.QueryRow("SELECT COUNT(*) FROM knowledge_entries").Scan(&got[1]) != nil {
			t.Fatal("count failed")
		}
		files, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		got[2] = len(files)
		staged, err := os.ReadDir(store.staging())
		if err != nil {
			t.Fatal(err)
		}
		got[3] = len(staged)
		return got
	}
	var classA, teacher uint64
	if pool.QueryRow("SELECT class_id,id FROM users WHERE username='teacher_a'").Scan(&classA, &teacher) != nil {
		t.Fatal("identity failed")
	}
	var uploaded []uint64
	t.Run("T5.1-T5.2-valid-samples-and-isolation", func(t *testing.T) {
		for _, name := range []string{"class-a-intro.txt", "class-a-notes.md"} {
			body, _ := fixtures.Files.ReadFile(name)
			w := upload("teacher_a", "../../"+name, body)
			var reply map[string]uint64
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || len(reply) != 1 || reply["material_id"] == 0 {
				t.Fatal("201 contract failed")
			}
			id := reply["material_id"]
			uploaded = append(uploaded, id)
			var classID, uploader uint64
			var source, key, text, nameSaved string
			if pool.QueryRow("SELECT m.class_id,m.uploader_id,m.source,m.storage_key,m.original_name,k.extracted_text FROM materials m JOIN knowledge_entries k ON k.material_id=m.id WHERE m.id=?", id).Scan(&classID, &uploader, &source, &key, &nameSaved, &text) != nil {
				t.Fatal("persisted records missing")
			}
			if classID != classA || uploader != teacher || source != "upload" || text != string(body) || nameSaved != name || !safeStorageKey(key) || !strings.HasPrefix(key, fmt.Sprint(id)+"-") {
				t.Fatal("persisted metadata wrong")
			}
			saved, err := os.ReadFile(filepath.Join(directory, key))
			if err != nil || string(saved) != string(body) {
				t.Fatal("original not saved")
			}
			for _, identity := range []string{"teacher_a", "student_a1"} {
				for _, suffix := range []string{"", "/file"} {
					if get(fmt.Sprintf("/api/materials/%d%s", id, suffix), cookies[identity]).Code != 200 {
						t.Fatal("same class cannot read immediately")
					}
				}
			}
			if get(fmt.Sprintf("/api/materials/%d/file", id), cookies["student_b1"]).Code != 404 {
				t.Fatal("B read A")
			}
		}
		for _, identity := range []string{"teacher_a", "student_a1", "student_b1"} {
			w := get("/api/materials?class_id=999", cookies[identity])
			var body struct {
				Items []materialView `json:"items"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
				t.Fatal("list failed")
			}
			expected := 2
			if identity == "student_b1" {
				expected = 1
			}
			if len(body.Items) != expected {
				t.Fatal("list isolation failed")
			}
		}
	})
	t.Run("T5.2-rejections-have-no-effects", func(t *testing.T) {
		before := counts()
		for _, identity := range []string{"student_a1", "student_b1", ""} {
			r := uploadRequest(testUploadPart{"file", "a.txt", []byte("text")})
			auth(r, identity)
			r.Body = forbiddenBody{t}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			status := 403
			if identity == "" {
				status = 401
			}
			checkError(w, status)
		}
		r := uploadRequest(testUploadPart{"file", "a.txt", []byte("text")})
		auth(r, "teacher_a")
		r.Header.Del("X-CSRF-Token")
		r.Body = forbiddenBody{t}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "csrf_invalid") {
			t.Fatal("CSRF gate failed")
		}
		checkError(upload("teacher_a", "a.pdf", []byte("bad")), 400)
		checkError(upload("teacher_a", "a.txt", []byte(strings.Repeat("a", maxMaterial+1))), 413)
		if counts() != before {
			t.Fatal("rejection has side effects")
		}
	})
	t.Run("T5.3-determined-failures-rollback", func(t *testing.T) {
		for _, stage := range []string{"file", "file_written", "extract", "material", "knowledge", "move", "before_commit"} {
			t.Run(stage, func(t *testing.T) {
				before := counts()
				store.fault = func(point string) error {
					if point == stage {
						return errors.New("injected")
					}
					return nil
				}
				w := upload("teacher_a", "a.txt", []byte("failure"))
				store.fault = nil
				status := 503
				if stage == "file" || stage == "file_written" || stage == "extract" || stage == "move" {
					status = 500
				}
				checkError(w, status)
				if counts() != before {
					t.Fatal("partial record or file remains")
				}
			})
		}
	})
	t.Run("T5.3-real-database-write-errors", func(t *testing.T) {
		for _, table := range []string{"materials", "knowledge_entries"} {
			before := counts()
			// Table names are fixed test constants; no request input enters SQL.
			if _, err := pool.Exec("CREATE TRIGGER test_reject BEFORE INSERT ON " + table + " FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test failure'"); err != nil {
				t.Fatal(err)
			}
			w := upload("teacher_a", "a.txt", []byte("constraint"))
			if _, err := pool.Exec("DROP TRIGGER test_reject"); err != nil {
				t.Fatal(err)
			}
			checkError(w, 500)
			if counts() != before {
				t.Fatal("database write failure left partial data")
			}
		}
	})
	t.Run("T5.1-full-size-and-BOM-persisted", func(t *testing.T) {
		for _, body := range [][]byte{[]byte(strings.Repeat("a", maxMaterial)), []byte("\xef\xbb\xbf# BOM")} {
			w := upload("teacher_a", "size.MD", body)
			var reply map[string]uint64
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
				t.Fatalf("valid input failed: %d", w.Code)
			}
			id := reply["material_id"]
			if get(fmt.Sprintf("/api/materials/%d/file", id), cookies["teacher_a"]).Body.String() != string(body) {
				t.Fatal("original changed")
			}
			var detail struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(get(fmt.Sprintf("/api/materials/%d", id), cookies["student_a1"]).Body.Bytes(), &detail) != nil || detail.Text != strings.TrimPrefix(string(body), "\xef\xbb\xbf") {
				t.Fatal("text extraction differs")
			}
		}
	})
	t.Run("T5.3-compensation-and-recovery-failure", func(t *testing.T) {
		before := counts()
		attempts := 0
		store.fault = func(point string) error {
			if point == "cleanup" {
				attempts++
			}
			if point == "extract" || point == "cleanup" {
				return errors.New("injected")
			}
			return nil
		}
		checkError(upload("teacher_a", "a.txt", []byte("failure")), 500)
		if attempts != 1 || store.ready.Load() || counts()[3] != before[3]+1 {
			t.Fatal("compensation policy wrong")
		}
		for _, target := range []string{"/api/materials", "/api/me", "/api/csrf"} {
			checkError(get(target, cookies["teacher_a"]), 503)
		}
		if get("/health", nil).Code != 200 {
			t.Fatal("health blocked")
		}
		store.fault = func(point string) error {
			if point == "recovery_cleanup" {
				return errors.New("injected")
			}
			return nil
		}
		if store.Recover(context.Background()) == nil || store.ready.Load() {
			t.Fatal("cleanup failure marked ready")
		}
		store.fault = nil
		if store.Recover(context.Background()) != nil || counts() != before {
			t.Fatal("manual recovery failed")
		}
	})
	t.Run("T5.3-unknown-commit-preserves-then-reconciles", func(t *testing.T) {
		for _, committed := range []bool{true, false} {
			before := counts()
			store.commit = func(tx *sql.Tx) error {
				if committed {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				return errors.New("acknowledgement lost")
			}
			checkError(upload("teacher_a", "a.txt", []byte("unknown")), 503)
			store.commit = nil
			after := counts()
			if after[2] != before[2]+1 || store.ready.Load() {
				t.Fatal("unknown commit file lost or gate open")
			}
			if err := store.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			expected := before
			if committed {
				expected[0]++
				expected[1]++
				expected[2]++
			}
			if counts() != expected {
				t.Fatal("commit reconciliation wrong")
			}
		}
	})
	t.Run("T5.4-response-loss-and-independent-retry", func(t *testing.T) {
		before := counts()
		r := uploadRequest(testUploadPart{"file", "retry.md", []byte("# persistent")})
		auth(r, "teacher_a")
		handler.ServeHTTP(&lostResponse{make(http.Header)}, r)
		after := counts()
		if after[0] != before[0]+1 || after[1] != before[1]+1 || after[2] != before[2]+1 {
			t.Fatal("response failure undid commit")
		}
		var first uint64
		if pool.QueryRow("SELECT MAX(id) FROM materials").Scan(&first) != nil {
			t.Fatal("first ID missing")
		}
		w := upload("teacher_a", "retry.md", []byte("# persistent"))
		var reply map[string]uint64
		json.Unmarshal(w.Body.Bytes(), &reply)
		if w.Code != 201 || reply["material_id"] == first {
			t.Fatal("retry not independent")
		}
		for _, id := range []uint64{first, reply["material_id"]} {
			if get(fmt.Sprintf("/api/materials/%d/file", id), cookies["teacher_a"]).Body.String() != "# persistent" {
				t.Fatal("retry files not both intact")
			}
		}
	})
	t.Run("T5.3-recovery-waits-for-unresolved-transaction", func(t *testing.T) {
		tx, err := pool.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		res, err := tx.Exec("INSERT INTO materials (class_id,uploader_id,original_name,storage_key,size,source) VALUES (?,?,'pending.txt','pending-test',4,'upload')", classA, teacher)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		key := fmt.Sprintf("%d-test.txt", id)
		if _, err = tx.Exec("UPDATE materials SET storage_key=? WHERE id=?", key, id); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("INSERT INTO knowledge_entries (material_id,class_id,extracted_text) VALUES (?,?,'held')", id, classA); err != nil {
			t.Fatal(err)
		}
		if os.WriteFile(filepath.Join(directory, key), []byte("held"), 0600) != nil {
			t.Fatal("file setup failed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if store.Recover(ctx) == nil {
			t.Fatal("recovery ignored pending transaction")
		}
		if _, err = os.Stat(filepath.Join(directory, key)); err != nil {
			t.Fatal("pending file deleted")
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = store.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(directory, key)); err != nil {
			t.Fatal("committed file deleted")
		}
	})
}
