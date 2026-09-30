package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/scrypt"
)

const loginBodyLimit = 16 * 1024

type loginInput struct{ Username, Password string }
type userIdentity struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	ClassID  uint64 `json:"class_id"`
}

// loginReply returns the signed token next to the account it identifies. No
// session cookie is set: the client stores the token and sends it back in the
// Authorization header.
type loginReply struct {
	User      userIdentity `json:"user"`
	Token     string       `json:"token"`
	ExpiresAt string       `json:"expires_at"`
}

func parseLogin(r *http.Request) (loginInput, bool) {
	var input loginInput
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return input, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, loginBodyLimit+1))
	if err != nil || len(body) > loginBodyLimit || !utf8.Valid(body) {
		return input, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return input, false
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return input, false
		}
		name, ok := key.(string)
		if !ok {
			return input, false
		}
		if _, exists := fields[name]; exists {
			return input, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return input, false
		}
		fields[name] = value
	}
	if _, err = decoder.Token(); err != nil {
		return input, false
	}
	if _, err = decoder.Token(); err != io.EOF {
		return input, false
	}
	if json.Unmarshal(fields["username"], &input.Username) != nil || json.Unmarshal(fields["password"], &input.Password) != nil {
		return input, false
	}
	u, p := utf8.RuneCountInString(input.Username), utf8.RuneCountInString(input.Password)
	return input, u >= 1 && u <= 64 && strings.TrimSpace(input.Username) != "" && p >= 1 && p <= 128
}

// Fixed parameters avoid trusting database strings as memory/work instructions.
// Even an unknown user performs one KDF; no default password is accepted.
func passwordMatches(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	salt, key := make([]byte, 16), make([]byte, 32)
	valid := len(parts) == 7
	if valid {
		valid = parts[0] == "" && parts[1] == "scrypt" && parts[2] == "32768" && parts[3] == "8" && parts[4] == "1"
		s, e1 := base64.RawStdEncoding.Strict().DecodeString(parts[5])
		k, e2 := base64.RawStdEncoding.Strict().DecodeString(parts[6])
		valid = valid && e1 == nil && e2 == nil && len(s) == 16 && len(k) == 32
		if valid {
			salt, key = s, k
		}
	}
	candidate, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	return err == nil && hmac.Equal(candidate, key) && valid
}

func loginError(w http.ResponseWriter, status int) {
	code, message := "invalid_request", "请求无效"
	if status == 401 {
		code, message = "invalid_credentials", "账号或密码错误"
	}
	jsonReply(w, status, map[string]string{"error": code, "message": message})
}

// login verifies the password and signs a token. Identity always comes from the
// account table; any role or class field in the request body is ignored.
func (s *Security) login(w http.ResponseWriter, r *http.Request) {
	input, ok := parseLogin(r)
	if !ok {
		loginError(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var user userIdentity
	var passwordHash string
	err := s.db.QueryRowContext(ctx, "SELECT id,username,role,class_id,password_hash FROM users WHERE username=?", input.Username).
		Scan(&user.ID, &user.Username, &user.Role, &user.ClassID, &passwordHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		securityError(w, 503)
		return
	}
	if !passwordMatches(passwordHash, input.Password) {
		loginError(w, 401)
		return
	}
	token, expires := s.issueToken(user, s.now().UTC().Truncate(time.Microsecond))
	if token == "" {
		securityError(w, 503)
		return
	}
	jsonReply(w, 200, loginReply{User: user, Token: token, ExpiresAt: expires.Format(time.RFC3339)})
}
