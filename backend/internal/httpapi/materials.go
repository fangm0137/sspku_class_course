package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type identityKey struct{}

// RequireIdentity ignores client identity fields and loads current account data.
func (s *Security) RequireIdentity(next http.Handler, teacherOnly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash := s.cookieHash(r)
		if hash == nil {
			securityError(w, 401)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var user userIdentity
		err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.role,u.class_id FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.session_id_hash=? AND s.expires_at>?`, hash, s.now().UTC()).Scan(&user.ID, &user.Username, &user.Role, &user.ClassID)
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

func apiError(w http.ResponseWriter, status int, code, message string) {
	jsonReply(w, status, map[string]string{"error": code, "message": message})
}
func materialNotFound(w http.ResponseWriter) { apiError(w, 404, "not_found", "资源不存在") }
func materialBroken(w http.ResponseWriter)   { apiError(w, 500, "internal_error", "服务内部错误") }

type materialView struct {
	ID        uint64 `json:"id"`
	Title     string `json:"title"`
	ClassID   uint64 `json:"class_id"`
	CreatedAt string `json:"created_at"`
}
type materialReader struct {
	db        *sql.DB
	directory string
}

func displayName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return "material"
	}
	return name
}
func materialID(r *http.Request) (uint64, bool) {
	value := r.PathValue("id")
	id, err := strconv.ParseUint(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}

func (m materialReader) list(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(identityKey{}).(userIdentity)
	rows, err := m.db.QueryContext(r.Context(), "SELECT id,original_name,class_id,created_at FROM materials WHERE class_id=? ORDER BY id", user.ClassID)
	if err != nil {
		securityError(w, 503)
		return
	}
	defer rows.Close()
	items := make([]materialView, 0)
	for rows.Next() {
		var item materialView
		var created time.Time
		if rows.Scan(&item.ID, &item.Title, &item.ClassID, &created) != nil {
			securityError(w, 503)
			return
		}
		item.Title = displayName(item.Title)
		item.CreatedAt = created.UTC().Format(time.RFC3339)
		items = append(items, item)
	}
	if rows.Err() != nil {
		securityError(w, 503)
		return
	}
	jsonReply(w, 200, struct {
		Items []materialView `json:"items"`
	}{items})
}

func (m materialReader) detail(w http.ResponseWriter, r *http.Request) {
	id, valid := materialID(r)
	if !valid {
		materialNotFound(w)
		return
	}
	user := r.Context().Value(identityKey{}).(userIdentity)
	var item materialView
	var created time.Time
	var text sql.NullString
	err := m.db.QueryRowContext(r.Context(), `SELECT m.id,m.original_name,m.class_id,m.created_at,k.extracted_text
 FROM materials m LEFT JOIN knowledge_entries k ON k.material_id=m.id AND k.class_id=m.class_id
 WHERE m.id=? AND m.class_id=?`, id, user.ClassID).Scan(&item.ID, &item.Title, &item.ClassID, &created, &text)
	if errors.Is(err, sql.ErrNoRows) {
		materialNotFound(w)
		return
	}
	if err != nil {
		securityError(w, 503)
		return
	}
	if !text.Valid {
		materialBroken(w)
		return
	}
	item.Title = displayName(item.Title)
	item.CreatedAt = created.UTC().Format(time.RFC3339)
	jsonReply(w, 200, struct {
		materialView
		Text string `json:"text"`
	}{item, text.String})
}

func safeStorageKey(key string) bool {
	if key == "" || key == "." || key == ".." {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (m materialReader) file(w http.ResponseWriter, r *http.Request) {
	id, valid := materialID(r)
	if !valid {
		materialNotFound(w)
		return
	}
	user := r.Context().Value(identityKey{}).(userIdentity)
	var name, key string
	var size int64
	err := m.db.QueryRowContext(r.Context(), "SELECT original_name,storage_key,size FROM materials WHERE id=? AND class_id=?", id, user.ClassID).Scan(&name, &key, &size)
	if errors.Is(err, sql.ErrNoRows) {
		materialNotFound(w)
		return
	}
	if err != nil {
		securityError(w, 503)
		return
	}
	const maximum = 5 * 1024 * 1024
	if !safeStorageKey(key) || size < 0 || size > maximum {
		materialBroken(w)
		return
	}
	root, err := os.OpenRoot(m.directory)
	if err != nil {
		materialBroken(w)
		return
	}
	defer root.Close()
	info, err := root.Lstat(key)
	if err != nil || !info.Mode().IsRegular() {
		materialBroken(w)
		return
	}
	file, err := root.Open(key)
	if err != nil {
		materialBroken(w)
		return
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		materialBroken(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(body)) != size {
		materialBroken(w)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": displayName(name)}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
