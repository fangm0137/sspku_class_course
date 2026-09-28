package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

func (s *Security) me(w http.ResponseWriter, r *http.Request) {
	hash := s.cookieHash(r)
	if hash == nil {
		securityError(w, 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var user userIdentity
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.role,u.class_id
 FROM sessions s JOIN users u ON u.id=s.user_id
 WHERE s.session_id_hash=? AND s.expires_at>?`, hash, s.now().UTC()).Scan(&user.ID, &user.Username, &user.Role, &user.ClassID)
	if errors.Is(err, sql.ErrNoRows) {
		securityError(w, 401)
		return
	}
	if err != nil {
		securityError(w, 503)
		return
	}
	jsonReply(w, 200, user)
}

// Recheck under a row lock so rotation/revocation cannot occur between check and delete.
func (s *Security) logout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		securityError(w, 503)
		return
	}
	defer tx.Rollback()
	hash := s.cookieHash(r)
	var user sql.NullInt64
	var stored []byte
	var expires time.Time
	var tokenExpires sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT user_id,expires_at,csrf_token_hash,csrf_expires_at
 FROM sessions WHERE session_id_hash=? FOR UPDATE`, hash).Scan(&user, &expires, &stored, &tokenExpires)
	if errors.Is(err, sql.ErrNoRows) {
		securityError(w, 401)
		return
	}
	if err != nil {
		securityError(w, 503)
		return
	}
	now := s.now().UTC()
	if !user.Valid || !expires.After(now) {
		securityError(w, 401)
		return
	}
	token, ok := decodeValue(r.Header.Get("X-CSRF-Token"))
	digest := sha256.Sum256(token)
	if !ok || !tokenExpires.Valid || !tokenExpires.Time.After(now) || !hmac.Equal(digest[:], stored) {
		securityError(w, 403)
		return
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE session_id_hash=?", hash); err != nil {
		securityError(w, 503)
		return
	}
	if err = tx.Commit(); err != nil {
		securityError(w, 503)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
	jsonReply(w, 200, map[string]bool{"ok": true})
}
