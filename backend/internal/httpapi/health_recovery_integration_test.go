package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/seed"
)

func TestMySQL8HealthRecovery(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run verify-schema.ps1 -Suite health")
	}
	cfg, err := config.LoadDatabase(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Clean(os.Getenv("TEST_MYSQL_DATA"))
	executable := os.Getenv("TEST_MYSQL_EXE")
	if !strings.HasPrefix(cfg.Name, "campusclaw_schema_verify_") || cfg.Host != "127.0.0.1" || !filepath.IsAbs(data) || !strings.HasPrefix(filepath.Base(filepath.Dir(data)), "campusclaw-schema-") || filepath.Base(data) != "data" || executable == "" {
		t.Fatal("requires isolated helper instance")
	}
	pool, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, password := randomValue()
	_, secret := randomValue()
	passwords, _ := seed.LoadPasswords(func(string) string { return password })
	if _, err = seed.Apply(context.Background(), pool, passwords); err != nil {
		t.Fatal("seed failed")
	}
	directory := filepath.Join(t.TempDir(), "uploads")
	if _, err = seed.ApplyMaterial(context.Background(), pool, directory); err != nil {
		t.Fatal("seed material failed")
	}
	store := newUploadStore(pool, directory)
	if store.Recover(context.Background()) != nil {
		t.Fatal("recovery failed")
	}
	handler := newHandler(pool, secret, store)
	// Protected calls carry a bearer token; nothing is stored in a cookie.
	get := func(target, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost"+target, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	loginRequest := httptest.NewRequest("POST", "http://localhost/api/login", strings.NewReader(loginJSON("student_b1", password)))
	loginRequest.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, loginRequest)
	if login.Code != 200 {
		t.Fatal("login failed")
	}
	var body struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(login.Body.Bytes(), &body) != nil || body.Token == "" {
		t.Fatal("login did not return a token")
	}
	token := body.Token
	before := get("/api/materials", token)
	if before.Code != 200 || !strings.Contains(before.Body.String(), "class-b-seed.txt") {
		t.Fatal("initial materials missing")
	}
	// Only the isolated root test connection can stop this disposable server.
	_, _ = pool.Exec("SHUTDOWN")
	stopped := false
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = pool.PingContext(ctx)
		cancel()
		if err != nil {
			stopped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !stopped {
		t.Fatal("isolated database did not stop")
	}
	health := get("/health", "")
	if health.Code != 200 || health.Body.String() != `{"status":"ok"}` {
		t.Fatal("health depends on DB")
	}
	failed := get("/api/materials", token)
	for i := 0; i < 50 && failed.Code == 200; i++ {
		time.Sleep(100 * time.Millisecond)
		failed = get("/api/materials", token)
	}
	if failed.Code != 503 || !strings.Contains(failed.Body.String(), "service_unavailable") {
		t.Fatalf("DB outage response status=%d, expected safe 503", failed.Code)
	}
	t.Log("PASS actual MySQL shutdown: health 200, authenticated materials 503")
	// Refusing connections precedes release of InnoDB files on Windows.
	fullyStopped := false
	for i := 0; i < 100; i++ {
		logBytes, _ := os.ReadFile(filepath.Join(filepath.Dir(data), "server.log"))
		if strings.Contains(string(logBytes), "Shutdown complete") {
			fullyStopped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !fullyStopped {
		t.Fatal("database did not finish releasing its data files")
	}
	restart := exec.Command(executable, "--no-defaults", "--datadir="+data, "--bind-address=127.0.0.1", "--port="+cfg.Port, "--mysqlx=0", "--skip-log-bin", "--general-log=0", "--log-error="+filepath.Join(filepath.Dir(data), "restarted.log"))
	if err = restart.Start(); err != nil {
		t.Fatal("isolated restart failed")
	}
	defer func() {
		_, _ = pool.Exec("SHUTDOWN")
		done := make(chan struct{})
		go func() { _ = restart.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = restart.Process.Kill()
			<-done
		}
	}()
	ready := false
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var one int
		err = pool.QueryRowContext(ctx, "SELECT 1").Scan(&one)
		cancel()
		if err == nil && one == 1 {
			ready = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatal("independent SELECT 1 readiness failed")
	}
	after := get("/api/materials", token)
	if after.Code != 200 || after.Body.String() != before.Body.String() {
		t.Fatal("same token or persisted materials lost after database restart")
	}
	t.Log("PASS MySQL restart: SELECT 1 ready; same token and persisted materials usable without login")
}
