package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 请求体解析是登录、提问、智能体、技能四条链路的共同入口，这里把它钉死：
// 只接受单个 JSON 对象，限长、限 UTF-8、拒绝未知字段与尾随内容。
func TestDecodeJSONRejectsMalformedBodies(t *testing.T) {
	type payload struct {
		Question string `json:"question"`
	}
	cases := []struct {
		name        string
		contentType string
		body        string
		ok          bool
	}{
		{"正常对象", "application/json", `{"question":"磁铁能吸木块吗？"}`, true},
		{"带字符集的 Content-Type", "application/json; charset=utf-8", `{"question":"你好"}`, true},
		{"缺少 Content-Type", "", `{"question":"你好"}`, false},
		{"非 JSON 的 Content-Type", "text/plain", `{"question":"你好"}`, false},
		{"未知字段", "application/json", `{"question":"你好","class_id":9}`, false},
		{"尾随内容", "application/json", `{"question":"你好"}{"question":"再来"}`, false},
		{"裸露的字符串", "application/json", `"你好"`, false},
		{"数组", "application/json", `["你好"]`, false},
		{"空体", "application/json", ``, false},
		{"非法 UTF-8", "application/json", "{\"question\":\"\xff\xfe\"}", false},
		{"超过长度上限", "application/json", `{"question":"` + strings.Repeat("字", jsonBodyLimit) + `"}`, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/ask", strings.NewReader(testCase.body))
			if testCase.contentType != "" {
				request.Header.Set("Content-Type", testCase.contentType)
			}
			var target payload
			got := decodeJSON(request, jsonBodyLimit, &target)
			if got != testCase.ok {
				t.Fatalf("decodeJSON = %v, want %v", got, testCase.ok)
			}
			if got && target.Question == "" {
				t.Fatal("decoded payload lost the question")
			}
		})
	}
}

// 路径里的 {id} 必须是规范的正整数：前导零、负数、非数字都按"不存在"处理，
// 这样同一个资源不会因为有多种写法而出现多种响应。
func TestIDFromPathAcceptsOnlyCanonicalPositiveIntegers(t *testing.T) {
	cases := map[string]bool{
		"1": true, "12": true, "9223372036854775807": true,
		"0": false, "007": false, "-1": false, "1.0": false, "abc": false, "": false, "1a": false,
	}
	for value, want := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/agents/x", nil)
		request.SetPathValue("id", value)
		id, ok := idFromPath(request)
		if ok != want {
			t.Errorf("idFromPath(%q) ok = %v, want %v", value, ok, want)
		}
		if ok && id == 0 {
			t.Errorf("idFromPath(%q) returned a zero id", value)
		}
	}
}
