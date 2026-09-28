package httpapi

import (
	"database/sql"
	"io"
	"net/http"
)

// Health does not touch the session database, even when it is unavailable.
func NewHandler(db *sql.DB, secret string, uploadDirectories ...string) http.Handler {
	directory := ""
	if len(uploadDirectories) > 0 {
		directory = uploadDirectories[0]
	}
	return newHandler(db, secret, newUploadStore(db, directory))
}

func newHandler(db *sql.DB, secret string, store *uploadStore) http.Handler {
	mux := http.NewServeMux()
	security := NewSecurity(db, secret)
	materials := materialReader{db: db, directory: store.directory}
	mux.Handle("POST /api/materials", security.RequireIdentity(security.ProtectWrite(http.HandlerFunc(store.upload), true), true))
	mux.Handle("GET /api/materials", security.RequireIdentity(http.HandlerFunc(materials.list), false))
	mux.Handle("GET /api/materials/{id}", security.RequireIdentity(http.HandlerFunc(materials.detail), false))
	mux.Handle("GET /api/materials/{id}/file", security.RequireIdentity(http.HandlerFunc(materials.file), false))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { materialNotFound(w) })
	mux.HandleFunc("GET /api/csrf", security.CSRF)
	mux.Handle("POST /api/login", security.ProtectWrite(http.HandlerFunc(security.login), false))
	mux.HandleFunc("GET /api/me", security.me)
	mux.Handle("POST /api/logout", security.ProtectWrite(http.HandlerFunc(security.logout), true))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && !store.ready.Load() {
			securityError(w, 503)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
