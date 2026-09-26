package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnonymousHealthNeedsNoDependencies(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"status":"ok"}` {
		t.Fatalf("unexpected liveness response: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/json" || len(recorder.Result().Cookies()) != 0 {
		t.Fatal("health must return JSON without creating a session")
	}
}
