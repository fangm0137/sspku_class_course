package httpapi

import (
	"context"
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

func TestMySQL8Frontend(t *testing.T) {
	if os.Getenv("TEST_MYSQL8") != "1" {
		t.Skip("run verify-schema.ps1 -Suite frontend")
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
	var zone string
	if pool.QueryRow("SELECT @@session.time_zone").Scan(&zone) != nil || zone != "+00:00" {
		t.Fatal("database session must use UTC")
	}
	_, password := randomValue()
	_, secret := randomValue()
	passwords, err := seed.LoadPasswords(func(string) string { return password })
	if err != nil {
		t.Fatal("password setup failed")
	}
	if _, err = seed.Apply(context.Background(), pool, passwords); err != nil {
		t.Fatal("account setup failed")
	}
	directory := filepath.Join(t.TempDir(), "uploads")
	if _, err = seed.ApplyMaterial(context.Background(), pool, directory); err != nil {
		t.Fatal("material setup failed")
	}
	store := newUploadStore(pool, directory)
	if store.Recover(context.Background()) != nil {
		t.Fatal("recovery failed")
	}
	handler := newHandler(pool, secret, store)
	server := httptest.NewServer(handler)
	defer server.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	frontend, err := filepath.Abs("../../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(frontend, "tests", "browser.mjs"))
	command.Dir = frontend
	command.Env = append(os.Environ(), "CAMPUSCLAW_API_TARGET="+server.URL, "CAMPUSCLAW_TEST_TLS="+secure.URL, "CAMPUSCLAW_TEST_PASSWORD="+password)
	// Verifier emits only stage names and safe failures, never credentials.
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal("browser verification failed")
	}
}
