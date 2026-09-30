package config

import (
	"os"
	"strings"
	"testing"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"UPLOAD_DIR":   os.TempDir(),
		"TOKEN_SECRET": "test-only-not-a-real-secret", "DB_HOST": "db.invalid",
		"DB_PORT": "3306", "DB_NAME": "test_db", "DB_USER": "test_user",
		"DB_PASSWORD": "test-only-not-a-real-password",
	}
}

func TestLoadRejectsMissingEmptyAndWhitespaceRequiredValues(t *testing.T) {
	for key := range validEnvironment() {
		for _, value := range []string{"", " \t\n"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				env := validEnvironment()
				env[key] = value
				_, err := Load(func(k string) string { return env[k] })
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("expected error identifying %s", key)
				}
				for _, secretKey := range []string{"TOKEN_SECRET", "DB_PASSWORD"} {
					if strings.Contains(err.Error(), validEnvironment()[secretKey]) {
						t.Fatal("configuration error leaked a credential")
					}
				}
			})
		}
	}
	_, err := Load(func(string) string { return "" })
	if err == nil {
		t.Fatal("empty environment accepted")
	}
}

func TestLoadValidConfigurationPreservesSecrets(t *testing.T) {
	env := validEnvironment()
	env["DB_PASSWORD"] = " password with spaces "
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.Database.Password != env["DB_PASSWORD"] || cfg.TokenSecret != env["TOKEN_SECRET"] {
		t.Fatal("configuration values changed unexpectedly")
	}
}

func TestLoadRejectsInvalidPortsWithoutEchoingValues(t *testing.T) {
	for _, key := range []string{"DB_PORT", "HTTP_ADDR"} {
		for _, value := range []string{"sensitive-invalid-value", "-1", "0", "65536"} {
			t.Run(key+value, func(t *testing.T) {
				env := validEnvironment()
				env[key] = value
				if key == "HTTP_ADDR" {
					env[key] = "127.0.0.1:" + value
				}
				_, err := Load(func(k string) string { return env[k] })
				if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "sensitive-invalid-value") {
					t.Fatal("expected safe validation error")
				}
			})
		}
	}
}
