package httpapi

import (
	"database/sql"
	"io"
	"net/http"
)

// Health does not touch the session database, even when it is unavailable.
func NewHandler(db *sql.DB, secret string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/csrf", NewSecurity(db, secret).CSRF)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	return mux
}
