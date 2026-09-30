package httpapi

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

// Security owns account authentication: it signs and verifies bearer tokens and
// resolves the account behind them. It does not implement roles or class rules.
type Security struct {
	db     *sql.DB
	secret []byte
	now    func() time.Time
}

func NewSecurity(db *sql.DB, secret string) *Security {
	return &Security{db: db, secret: []byte(secret), now: time.Now}
}

// randomValue returns 32 random bytes and their unpadded base64url encoding.
// Fixtures use it for generated passwords and test-only secrets.
func randomValue() ([]byte, string) {
	b := make([]byte, 32)
	rand.Read(b)
	return b, base64.RawURLEncoding.EncodeToString(b)
}

func jsonReply(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func securityError(w http.ResponseWriter, status int) {
	code, message := "service_unavailable", "服务暂不可用"
	if status == 401 {
		code, message = "unauthenticated", "请先登录"
	}
	if status == 403 {
		code, message = "forbidden", "无权执行此操作"
	}
	jsonReply(w, status, map[string]string{"error": code, "message": message})
}
