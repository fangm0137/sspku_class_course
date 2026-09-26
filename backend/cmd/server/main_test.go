package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testEnvironment() map[string]string {
	return map[string]string{
		"SESSION_SECRET": "test-only-secret", "DB_HOST": "unreachable.invalid",
		"DB_PORT": "3306", "DB_NAME": "test_db", "DB_USER": "test_user", "DB_PASSWORD": "test-only-password",
	}
}

func TestMissingConfigurationPreventsListening(t *testing.T) {
	for key := range testEnvironment() {
		t.Run(key, func(t *testing.T) {
			env := testEnvironment()
			delete(env, key)
			err := run(func(k string) string { return env[k] }, func(*http.Server) error {
				t.Fatal("server started despite missing configuration")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "test-only") {
				t.Fatal("expected safe startup error")
			}
		})
	}
}

func TestServerStartsWithUnreachableDatabase(t *testing.T) {
	env := testEnvironment()
	called := false
	err := run(func(k string) string { return env[k] }, func(server *http.Server) error {
		called = true
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
		if response.Code != 200 || response.Body.String() != `{"status":"ok"}` {
			t.Fatal("liveness depends on database availability")
		}
		return http.ErrServerClosed
	})
	if err != nil || !called {
		t.Fatalf("server did not start: %v", err)
	}
}
