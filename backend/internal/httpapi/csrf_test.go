package httpapi

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, target, origin, site string
		required, want             bool
	}{
		{"bootstrap", "http://localhost:8080/api/csrf", "", "same-origin", false, true},
		{"missing-write", "http://localhost:8080/test", "", "same-origin", true, false},
		{"same", "http://localhost:8080/test", "http://localhost:8080", "same-origin", true, true},
		{"host-case-default-port", "http://localhost/test", "http://LOCALHOST:80", "", true, true},
		{"https", "https://localhost/test", "https://localhost:443", "", true, true},
		{"port", "http://localhost:8080/test", "http://localhost:8081", "", true, false},
		{"scheme", "http://localhost/test", "https://localhost", "", true, false},
		{"suffix", "http://localhost/test", "http://localhost.evil.test", "", true, false},
		{"opaque", "http://localhost/test", "null", "", true, false},
		{"path", "http://localhost/test", "http://localhost/evil", "", true, false},
		{"empty-port", "http://localhost/test", "http://localhost:", "", true, false},
		{"empty-fragment", "http://localhost/test", "http://localhost#", "", true, false},
		{"invalid-port", "http://localhost:65536/test", "http://localhost:65536", "", true, false},
		{"userinfo", "http://localhost/test", "http://evil@localhost", "", true, false},
		{"multiple", "http://localhost/test", "http://localhost http://evil.test", "", true, false},
		{"cross-site-get", "http://localhost/api/csrf", "", "cross-site", false, false},
		{"same-site-is-not-same-origin", "http://localhost/api/csrf", "", "same-site", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.target, nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if sameOrigin(r, tc.required) != tc.want {
				t.Fatal("unexpected origin decision")
			}
		})
	}
	r := httptest.NewRequest("POST", "http://localhost/test", nil)
	r.Header.Add("Origin", "http://localhost")
	r.Header.Add("Origin", "http://localhost")
	if sameOrigin(r, true) {
		t.Fatal("duplicate Origin accepted")
	}
	r.Header.Set("Origin", "https://localhost")
	r.Header.Set("X-Forwarded-Proto", "https")
	if sameOrigin(r, true) {
		t.Fatal("untrusted forwarded scheme accepted")
	}
	r.TLS = &tls.ConnectionState{}
	if !sameOrigin(r, true) {
		t.Fatal("direct TLS rejected")
	}
}

func TestCrossOriginBootstrapDoesNotUseDatabase(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/api/csrf", nil)
	r.Header.Set("Origin", "http://evil.test")
	w := httptest.NewRecorder()
	NewHandler(nil, "").ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
		t.Fatal("unsafe cross-origin response")
	}
}
