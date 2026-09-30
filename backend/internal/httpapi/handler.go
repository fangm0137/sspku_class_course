package httpapi

import (
	"database/sql"
	"io"
	"net/http"
	"os"

	"campusclaw/backend/internal/rag"
)

// Health does not touch the account database, even when it is unavailable.
func NewHandler(db *sql.DB, secret string, uploadDirectories ...string) http.Handler {
	directory := ""
	if len(uploadDirectories) > 0 {
		directory = uploadDirectories[0]
	}
	return newHandler(db, secret, newUploadStore(db, directory), rag.New(db, rag.FromEnv(os.Getenv)))
}

// newHandler wires every route behind bearer token authentication. Only login
// and the liveness probe stay anonymous; GET /api/csrf is intentionally gone,
// so unknown API paths fall through to a plain 404. ragService 允许为空，
// 此时问答按依赖不可用处理，其余接口不受影响。
func newHandler(db *sql.DB, secret string, store *uploadStore, ragServices ...*rag.Service) http.Handler {
	ragService := (*rag.Service)(nil)
	if len(ragServices) > 0 {
		ragService = ragServices[0]
	}
	mux := http.NewServeMux()
	security := NewSecurity(db, secret)
	materials := materialReader{db: db, directory: store.directory}
	ask := askHandler{service: ragService}
	search := searchHandler{service: ragService}
	reindex := reindexHandler{service: ragService}
	mux.Handle("POST /api/ask", security.RequireIdentity(http.HandlerFunc(ask.ask), false))
	mux.Handle("GET /api/search", security.RequireIdentity(http.HandlerFunc(search.search), false))
	mux.Handle("POST /api/materials", security.RequireIdentity(http.HandlerFunc(store.upload), true))
	mux.Handle("POST /api/materials/{id}/reindex", security.RequireIdentity(http.HandlerFunc(reindex.reindex), true))
	mux.Handle("GET /api/materials", security.RequireIdentity(http.HandlerFunc(materials.list), false))
	mux.Handle("GET /api/materials/{id}", security.RequireIdentity(http.HandlerFunc(materials.detail), false))
	mux.Handle("GET /api/materials/{id}/file", security.RequireIdentity(http.HandlerFunc(materials.file), false))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { materialNotFound(w) })
	mux.Handle("POST /api/login", http.HandlerFunc(security.login))
	mux.Handle("GET /api/me", security.RequireIdentity(http.HandlerFunc(security.me), false))
	mux.Handle("POST /api/logout", security.RequireIdentity(http.HandlerFunc(security.logout), false))
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
