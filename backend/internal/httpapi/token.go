package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const tokenLifetime = 8 * time.Hour
const bearerScheme = "Bearer "

// tokenHeader is the only accepted JOSE protected header. Pinning it means a
// token can never pick its own verification routine, which rules out "none" and
// algorithm confusion attacks before any signature work happens.
const tokenHeader = `{"alg":"HS256","typ":"JWT"}`

// tokenClaims carries the account identity. Role and class are recorded for
// display only: RequireIdentity always reloads them from the account table.
type tokenClaims struct {
	Subject   uint64 `json:"sub"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	ClassID   uint64 `json:"classId"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type identityKey struct{}

func encodeSegment(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *Security) signSegment(message string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

// issueToken signs a compact JWS for the account and reports its expiry.
func (s *Security) issueToken(user userIdentity, now time.Time) (string, time.Time) {
	expires := now.Add(tokenLifetime)
	payload, err := json.Marshal(tokenClaims{
		Subject:   user.ID,
		Username:  user.Username,
		Role:      user.Role,
		ClassID:   user.ClassID,
		IssuedAt:  now.Unix(),
		ExpiresAt: expires.Unix(),
	})
	if err != nil {
		return "", time.Time{}
	}
	signing := encodeSegment([]byte(tokenHeader)) + "." + encodeSegment(payload)
	return signing + "." + encodeSegment(s.signSegment(signing)), expires
}

// verifyToken accepts only an HS256 token signed with this server's key whose
// expiry is still in the future. Every deviation reports ok = false so callers
// answer a uniform 401.
func (s *Security) verifyToken(raw string, now time.Time) (tokenClaims, bool) {
	segments := strings.Split(raw, ".")
	if len(segments) != 3 {
		return tokenClaims{}, false
	}
	header, err := base64.RawURLEncoding.Strict().DecodeString(segments[0])
	if err != nil || string(header) != tokenHeader {
		return tokenClaims{}, false
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(segments[2])
	if err != nil || len(signature) != sha256.Size {
		return tokenClaims{}, false
	}
	if !hmac.Equal(s.signSegment(segments[0]+"."+segments[1]), signature) {
		return tokenClaims{}, false
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(segments[1])
	if err != nil {
		return tokenClaims{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var claims tokenClaims
	if decoder.Decode(&claims) != nil {
		return tokenClaims{}, false
	}
	if _, err = decoder.Token(); err == nil {
		return tokenClaims{}, false
	}
	if claims.Subject == 0 || claims.ExpiresAt <= now.Unix() {
		return tokenClaims{}, false
	}
	return claims, true
}

// bearerToken requires exactly one Authorization header using the Bearer scheme.
func bearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	if !strings.HasPrefix(values[0], bearerScheme) {
		return "", false
	}
	token := values[0][len(bearerScheme):]
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

// RequireIdentity verifies the bearer token and then loads the authoritative
// account row, so client supplied roles or classes can never widen access.
func (s *Security) RequireIdentity(next http.Handler, teacherOnly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			securityError(w, 401)
			return
		}
		claims, ok := s.verifyToken(raw, s.now().UTC())
		if !ok {
			securityError(w, 401)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var user userIdentity
		err := s.db.QueryRowContext(ctx, "SELECT id,username,role,class_id FROM users WHERE id=?", claims.Subject).
			Scan(&user.ID, &user.Username, &user.Role, &user.ClassID)
		if errors.Is(err, sql.ErrNoRows) {
			securityError(w, 401)
			return
		}
		if err != nil {
			securityError(w, 503)
			return
		}
		if teacherOnly && user.Role != "teacher" {
			apiError(w, 403, "forbidden", "无权执行此操作")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, identityKey{}, user)))
	})
}
