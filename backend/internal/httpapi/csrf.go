package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const sessionCookie = "campusclaw_session"
const csrfLifetime = 30 * time.Minute

// Security owns session-backed CSRF checks. It does not implement login or roles.
type Security struct {
	db     *sql.DB
	secret []byte
	now    func() time.Time
}

func NewSecurity(db *sql.DB, secret string) *Security {
	return &Security{db: db, secret: []byte(secret), now: time.Now}
}

func randomValue() ([]byte, string) {
	b := make([]byte, 32)
	rand.Read(b)
	return b, base64.RawURLEncoding.EncodeToString(b)
}

func decodeValue(value string) ([]byte, bool) {
	if len(value) != 43 {
		return nil, false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return b, err == nil && len(b) == 32
}

func (s *Security) digest(id []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(id)
	return mac.Sum(nil)
}

func (s *Security) cookieHash(r *http.Request) []byte {
	cookies := r.CookiesNamed(sessionCookie)
	if len(cookies) != 1 {
		return nil
	}
	id, ok := decodeValue(cookies[0].Value)
	if !ok {
		return nil
	}
	return s.digest(id)
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
		code, message = "csrf_invalid", "请求校验失败"
	}
	jsonReply(w, status, map[string]string{"error": code, "message": message})
}

func originParts(raw string) (string, string, string, bool) {
	if strings.ContainsAny(raw, "?#") {
		return "", "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Hostname() == "" {
		return "", "", "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", "", false
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", "", "", false
	}
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", "", "", false
	}
	return u.Scheme, strings.ToLower(u.Hostname()), strconv.Itoa(n), true
}

func sameOrigin(r *http.Request, required bool) bool {
	sites := r.Header.Values("Sec-Fetch-Site")
	if len(sites) > 1 || (len(sites) == 1 && sites[0] != "same-origin" && sites[0] != "none") {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return !required
	}
	if len(origins) != 1 {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	a, b, c, ok := originParts(origins[0])
	x, y, z, valid := originParts(scheme + "://" + r.Host)
	return ok && valid && a == x && b == y && c == z
}

// CSRF issues or rotates a token only after the database transaction commits.
func (s *Security) CSRF(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r, false) {
		securityError(w, 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	now := s.now().UTC().Truncate(time.Microsecond)
	token, encodedToken := randomValue()
	tokenHash := sha256.Sum256(token)
	id, encodedID := randomValue()
	expires := now.Add(csrfLifetime)
	hash := s.cookieHash(r)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		securityError(w, 503)
		return
	}
	defer tx.Rollback()
	existing := false
	if hash != nil {
		err = tx.QueryRowContext(ctx, "SELECT expires_at FROM sessions WHERE session_id_hash = ? AND expires_at > ? FOR UPDATE", hash, now).Scan(&expires)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			securityError(w, 503)
			return
		}
		existing = err == nil
	}
	if !existing {
		expires = now.Add(csrfLifetime)
		hash = s.digest(id)
		_, err = tx.ExecContext(ctx, "INSERT INTO sessions (session_id_hash, user_id, created_at, expires_at, csrf_token_hash, csrf_expires_at) VALUES (?, NULL, ?, ?, ?, ?)", hash, now, expires, tokenHash[:], expires)
	} else {
		tokenExpires := now.Add(csrfLifetime)
		if expires.Before(tokenExpires) {
			tokenExpires = expires
		}
		_, err = tx.ExecContext(ctx, "UPDATE sessions SET csrf_token_hash = ?, csrf_expires_at = ? WHERE session_id_hash = ?", tokenHash[:], tokenExpires, hash)
	}
	if err != nil {
		securityError(w, 503)
		return
	}
	if err = tx.Commit(); err != nil {
		securityError(w, 503)
		return
	}
	if !existing {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: encodedID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, Expires: expires, MaxAge: 1800})
	}
	jsonReply(w, 200, map[string]string{"csrf_token": encodedToken})
}

// ProtectWrite checks identity first when required, then origin and the bound token.
// Until later tasks register business routes it is exercised with test handlers only.
func (s *Security) ProtectWrite(next http.Handler, requireLogin bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireLogin && !sameOrigin(r, true) {
			securityError(w, 403)
			return
		}
		status := 403
		if requireLogin {
			status = 401
		}
		hash := s.cookieHash(r)
		if hash == nil {
			securityError(w, status)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		now := s.now().UTC()
		var user sql.NullInt64
		var stored []byte
		var expiry sql.NullTime
		err := s.db.QueryRowContext(ctx, "SELECT user_id, csrf_token_hash, csrf_expires_at FROM sessions WHERE session_id_hash = ? AND expires_at > ?", hash, now).Scan(&user, &stored, &expiry)
		if errors.Is(err, sql.ErrNoRows) {
			securityError(w, status)
			return
		}
		if err != nil {
			securityError(w, 503)
			return
		}
		if requireLogin && !user.Valid {
			securityError(w, 401)
			return
		}
		values := r.Header.Values("X-CSRF-Token")
		if !sameOrigin(r, true) || len(values) != 1 {
			securityError(w, 403)
			return
		}
		token, ok := decodeValue(values[0])
		candidate := sha256.Sum256(token)
		if !ok || !expiry.Valid || !expiry.Time.After(now) || !hmac.Equal(candidate[:], stored) {
			securityError(w, 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
