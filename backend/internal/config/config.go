package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Config is loaded once at startup. Do not log it: it contains credentials.
type Config struct {
	HTTPAddr      string
	SessionSecret string
	Database      Database
}

type Database struct {
	Host     string
	Port     string
	Name     string
	User     string
	Password string
}

// Load accepts a lookup function so tests do not need real credentials.
// Errors identify configuration keys only, never their values.
func Load(getenv func(string) string) (Config, error) {
	if strings.TrimSpace(getenv("SESSION_SECRET")) == "" {
		return Config{}, fmt.Errorf("required configuration missing: SESSION_SECRET")
	}
	database, err := LoadDatabase(getenv)
	if err != nil {
		return Config{}, err
	}
	addr := getenv("HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	_, listenPort, err := net.SplitHostPort(addr)
	if err != nil {
		return Config{}, fmt.Errorf("invalid configuration: HTTP_ADDR must be host:port")
	}
	p, err := strconv.Atoi(listenPort)
	if err != nil || p < 1 || p > 65535 {
		return Config{}, fmt.Errorf("invalid configuration: HTTP_ADDR port must be between 1 and 65535")
	}
	return Config{
		HTTPAddr: addr, SessionSecret: getenv("SESSION_SECRET"),
		Database: database,
	}, nil
}

// LoadDatabase serves migrations without requiring a session secret.
func LoadDatabase(getenv func(string) string) (Database, error) {
	for _, key := range []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD"} {
		if strings.TrimSpace(getenv(key)) == "" {
			return Database{}, fmt.Errorf("required configuration missing: %s", key)
		}
	}
	port, err := strconv.Atoi(getenv("DB_PORT"))
	if err != nil || port < 1 || port > 65535 {
		return Database{}, fmt.Errorf("invalid configuration: DB_PORT must be between 1 and 65535")
	}
	return Database{Host: getenv("DB_HOST"), Port: getenv("DB_PORT"), Name: getenv("DB_NAME"), User: getenv("DB_USER"), Password: getenv("DB_PASSWORD")}, nil
}
