package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// searchRequest 直接构造带可信身份的请求，绕开会话校验，只验证检索接口自身的入参校验。
func searchRequest(target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	identity := userIdentity{ID: 1, Username: "teacher_a", Role: "teacher", ClassID: 1}
	r = r.WithContext(context.WithValue(r.Context(), identityKey{}, identity))
	w := httptest.NewRecorder()
	searchHandler{}.search(w, r)
	return w
}

func TestSearchValidatesQueryAndModeBeforeDependencies(t *testing.T) {
	for name, target := range map[string]string{
		"missing-query": "/api/search",
		"blank-query":   "/api/search?q=%20%20&mode=keyword",
		"too-long":      "/api/search?q=" + strings.Repeat("%E9%97%AE", searchQueryLimit+1),
		"bad-mode":      "/api/search?q=%E7%A3%81%E9%93%81&mode=semantic",
	} {
		t.Run(name, func(t *testing.T) {
			if w := searchRequest(target); w.Code != 400 {
				t.Fatalf("got status %d, want 400", w.Code)
			}
		})
	}
	// 参数合法但检索依赖未接入时必须是 503，而不是 200 空命中。
	for _, mode := range []string{"keyword", "vector", "hybrid", ""} {
		target := "/api/search?q=%E7%A3%81%E9%93%81"
		if mode != "" {
			target += "&mode=" + mode
		}
		if w := searchRequest(target); w.Code != 503 {
			t.Fatalf("mode %q: got status %d, want 503", mode, w.Code)
		}
	}
}

func TestParseReindexAcceptsDefaultsAndRejectsUnknownFields(t *testing.T) {
	for name, body := range map[string]struct {
		raw  string
		want bool
	}{
		"empty-body":        {"", true},
		"blank-body":        {"   ", true},
		"auto":              {`{"strategy":"auto"}`, true},
		"custom":            {`{"strategy":"custom","size":600,"overlap":60,"collapse_whitespace":true}`, true},
		"unknown-field":     {`{"strategy":"auto","chunk_size":10}`, false},
		"wrong-type":        {`{"strategy":12}`, false},
		"broken":            {`{"strategy":`, false},
		"trailing-object":   {`{"strategy":"auto"} {}`, false},
		"oversized-payload": {`{"strategy":"` + strings.Repeat("a", reindexBodyLimit) + `"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/materials/1/reindex", strings.NewReader(body.raw))
			r.Header.Set("Content-Type", "application/json")
			if _, ok := parseReindex(r); ok != body.want {
				t.Fatalf("accepted = %v, want %v", ok, body.want)
			}
		})
	}
}

func TestReindexStrategyValidation(t *testing.T) {
	handler := reindexHandler{}
	for name, body := range map[string]struct {
		raw    string
		status int
	}{
		"invalid-strategy": {`{"strategy":"semantic"}`, 400},
		"size-too-small":   {`{"strategy":"custom","size":99}`, 400},
		"size-too-large":   {`{"strategy":"custom","size":2001}`, 400},
		"overlap-too-wide": {`{"strategy":"custom","size":200,"overlap":150}`, 400},
		// 参数合法但检索模块未接入：依赖不可用。
		"dependency":       {`{"strategy":"hierarchy"}`, 503},
		"bad-request-body": {`{"strategy":`, 400},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/materials/1/reindex", strings.NewReader(body.raw))
			r.SetPathValue("id", "1")
			r.Header.Set("Content-Type", "application/json")
			identity := userIdentity{ID: 1, Username: "teacher_a", Role: "teacher", ClassID: 1}
			r = r.WithContext(context.WithValue(r.Context(), identityKey{}, identity))
			w := httptest.NewRecorder()
			handler.reindex(w, r)
			if w.Code != body.status {
				t.Fatalf("got status %d, want %d (%s)", w.Code, body.status, w.Body.String())
			}
		})
	}
}

func TestRoundScoreKeepsReadablePrecision(t *testing.T) {
	if got := roundScore(0.1234567891); got != 0.123457 {
		t.Fatalf("got %v", got)
	}
}
