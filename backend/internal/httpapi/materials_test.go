package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestMaterialBoundaries(t *testing.T) {
	for _, value := range []string{"", "0", "01", "-1", "+1", "abc", "18446744073709551616"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("id", value)
		if _, ok := materialID(r); ok {
			t.Fatal("invalid ID accepted")
		}
	}
	for _, key := range []string{"../secret", "..", "/etc/passwd", `C:\secret`, "a/b", "a:b", "a\x00b"} {
		if safeStorageKey(key) {
			t.Fatal("unsafe key accepted")
		}
	}
	if displayName("../../private/notes\r\n.txt") != "notes.txt" || displayName(`C:\private\notes.md`) != "notes.md" {
		t.Fatal("filename exposes path/control characters")
	}
}
