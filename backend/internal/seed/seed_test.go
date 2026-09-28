package seed

import (
	"context"
	"strings"
	"testing"
)

func TestPasswordValidationBeforeDatabase(t *testing.T) {
	for _, key := range passwordKeys {
		for _, bad := range []string{"", " \t\n", strings.Repeat("密", 129), string([]byte{255})} {
			_, err := LoadPasswords(func(k string) string {
				if k == key {
					return bad
				}
				return "synthetic-test-only"
			})
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "synthetic-test-only") {
				t.Fatal("expected safe configuration error")
			}
		}
	}
	for _, good := range []string{"密", strings.Repeat("密", 128), " password with spaces "} {
		p, err := LoadPasswords(func(string) string { return good })
		if err != nil || p.values[0] != good {
			t.Fatal("valid password changed or rejected")
		}
	}
	if _, err := Apply(context.Background(), nil, Passwords{}); err == nil {
		t.Fatal("zero value accepted")
	}
}
