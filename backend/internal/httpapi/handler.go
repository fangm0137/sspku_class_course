package httpapi

import (
	"io"
	"net/http"
)

// NewHandler has no database, file storage, or authentication dependency.
// Only the T1.1 liveness endpoint is implemented at this stage.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	return mux
}
