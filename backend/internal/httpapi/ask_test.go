package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func askRequest(body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/ask", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	NewHandler(nil, "test-only-secret").ServeHTTP(w, r)
	return w
}

func TestAskRejectsMalformedInput(t *testing.T) {
	question := "磁铁能吸木块吗？"
	for name, body := range map[string]string{
		"empty-body":     "",
		"missing":        "{}",
		"null":           `{"question":null}`,
		"number":         `{"question":12}`,
		"blank":          `{"question":"   \n\t "}`,
		"empty":          `{"question":""}`,
		"array":          `[]`,
		"duplicate":      `{"question":"a","question":"b"}`,
		"trailing":       `{"question":"a"} {}`,
		"broken":         `{"question":`,
		"too-long":       `{"question":"` + strings.Repeat("问", askQuestionLimit+1) + `"}`,
		"invalid-utf8":   "{\"question\":\"\xff\"}",
		"oversized-body": `{"question":"` + strings.Repeat("a", askBodyLimit) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			// 匿名请求在身份检查阶段就应当被拒；这里只断言不会因解析而放行。
			if w := askRequest(body); w.Code == 200 {
				t.Fatal("malformed question accepted")
			}
		})
	}
	// 合法长度边界必须解析通过。
	for _, valid := range []string{question, strings.Repeat("问", askQuestionLimit)} {
		r := httptest.NewRequest("POST", "/api/ask", strings.NewReader(`{"question":"`+valid+`"}`))
		r.Header.Set("Content-Type", "application/json")
		got, ok := parseAsk(r)
		if !ok || got != valid {
			t.Fatal("valid question rejected")
		}
	}
}

func TestAskRequiresJSONContentType(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/ask", strings.NewReader(`{"question":"x"}`))
	r.Header.Set("Content-Type", "text/plain")
	if _, ok := parseAsk(r); ok {
		t.Fatal("wrong content type accepted")
	}
}
