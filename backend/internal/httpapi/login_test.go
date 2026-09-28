package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/scrypt"
)

func loginJSON(username, password string) string {
	b, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return string(b)
}

func fixturePasswordHash(t *testing.T, password string) string {
	t.Helper()
	raw, _ := randomValue()
	salt := raw[:16]
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		t.Fatal("fixture hashing failed")
	}
	return fmt.Sprintf("$scrypt$32768$8$1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func malformedLogins() map[string]string {
	return map[string]string{
		"empty-body": "", "missing": "{}", "null-object": "null", "array": "[]",
		"null-username": `{"username":null,"password":"x"}`, "null-password": `{"username":"x","password":null}`,
		"number": `{"username":123,"password":"x"}`, "bool": `{"username":"x","password":true}`,
		"empty-user": loginJSON("", "x"), "empty-password": loginJSON("x", ""), "blank-user": loginJSON(" \t　", "x"),
		"long-user": loginJSON(strings.Repeat("课", 65), "x"), "long-password": loginJSON("x", strings.Repeat("密", 129)),
		"broken": `{"username":`, "trailing": loginJSON("x", "x") + ` {}`,
		"duplicate":    `{"username":"x","username":"y","password":"x"}`,
		"wrong-case":   `{"Username":"x","password":"x"}`,
		"invalid-utf8": "{\"username\":\"\xff\",\"password\":\"x\"}",
		"body-limit":   loginJSON("x", "x") + strings.Repeat(" ", loginBodyLimit),
	}
}

func TestLoginInput(t *testing.T) {
	for name, body := range malformedLogins() {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			if _, ok := parseLogin(r); ok {
				t.Fatal("malformed input accepted")
			}
		})
	}
	for _, pair := range [][2]string{{"课", "密"}, {strings.Repeat("课", 64), strings.Repeat("密", 128)}, {" x ", " "}} {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(loginJSON(pair[0], pair[1])))
		r.Header.Set("Content-Type", "application/json; charset=utf-8")
		input, ok := parseLogin(r)
		if !ok || input.Username != pair[0] || input.Password != pair[1] {
			t.Fatal("valid boundary changed or rejected")
		}
	}
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(loginJSON("x", "x")))
	r.Header.Set("Content-Type", "text/plain")
	if _, ok := parseLogin(r); ok {
		t.Fatal("wrong content type accepted")
	}
}

func TestPasswordVerification(t *testing.T) {
	_, password := randomValue()
	encoded := fixturePasswordHash(t, password)
	if !passwordMatches(encoded, password) || passwordMatches(encoded, password+"x") {
		t.Fatal("password verification incorrect")
	}
	for _, bad := range []string{"", "$scrypt$1073741824$8$1$AA$AA", strings.Replace(encoded, "$32768$", "$16384$", 1), strings.Replace(encoded, "scrypt", "other", 1)} {
		if passwordMatches(bad, password) {
			t.Fatal("malformed/unsupported hash accepted")
		}
	}
}
