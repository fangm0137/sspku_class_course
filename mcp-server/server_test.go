package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "test-only-token"

func post(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是 JSON：%s", recorder.Body.String())
	}
	return payload
}

// 存活探针不需要令牌，也不透露工具。
func TestHealthNeedsNoTokenAndLeaksNothing(t *testing.T) {
	recorder := httptest.NewRecorder()
	newMux(testToken).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"unit_convert", "formula_hint", testToken} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("/health 泄露了 %q：%s", forbidden, body)
		}
	}
}

// 缺令牌、错误令牌、未配置令牌都必须是 401，且响应不含期望值。
func TestTokenIsRequired(t *testing.T) {
	cases := []struct {
		name     string
		server   string
		presente string
	}{
		{"缺少令牌", testToken, ""},
		{"错误令牌", testToken, "wrong-token"},
		{"服务端未配置令牌", "", testToken},
	}
	for _, item := range cases {
		recorder := post(t, newMux(item.server), item.presente, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s = %d, want 401", item.name, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), testToken) {
			t.Fatalf("%s 的响应体包含令牌", item.name)
		}
	}
}

// initialize 返回 serverInfo，供宿主确认对端确实是 MCP 服务。
func TestInitialize(t *testing.T) {
	recorder := post(t, newMux(testToken), testToken, `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	payload := decode(t, recorder)
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 result：%v", payload)
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok || info["name"] == "" {
		t.Fatalf("缺少 serverInfo：%v", result)
	}
	if payload["id"] != float64(7) {
		t.Fatalf("id = %v，应原样回显", payload["id"])
	}
}

// 至少两个工具，每个都带 name / description / inputSchema。
func TestToolsListCarriesSchemas(t *testing.T) {
	recorder := post(t, newMux(testToken), testToken, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	payload := decode(t, recorder)
	result := payload["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) < 2 {
		t.Fatalf("tools = %d, want >= 2", len(tools))
	}
	names := map[string]bool{}
	for _, item := range tools {
		tool := item.(map[string]any)
		name, _ := tool["name"].(string)
		names[name] = true
		if name == "" || name[0] < 'a' || name[0] > 'z' || strings.Contains(name, "-") {
			t.Fatalf("工具名不合规：%q", name)
		}
		if description, _ := tool["description"].(string); description == "" {
			t.Fatalf("%s 缺少 description", name)
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Fatalf("%s 的 inputSchema 不完整：%v", name, tool["inputSchema"])
		}
		if _, ok := schema["properties"].(map[string]any); !ok {
			t.Fatalf("%s 的 inputSchema 没有 properties", name)
		}
	}
	for _, want := range []string{"formula_hint", "unit_convert"} {
		if !names[want] {
			t.Fatalf("缺少工具 %s，实际：%v", want, names)
		}
	}
}

// 两个工具各调一次，都返回 content 文本数组且 isError 为假。
func TestToolsCall(t *testing.T) {
	handler := newMux(testToken)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"换算", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"unit_convert","arguments":{"value":3.2,"from":"公里","to":"米"}}}`, "3200"},
		{"提示", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"formula_hint","arguments":{"topic":"一元二次方程"}}}`, "判别式"},
	}
	for _, item := range cases {
		payload := decode(t, post(t, handler, testToken, item.body))
		result, ok := payload["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s 缺少 result：%v", item.name, payload)
		}
		if result["isError"] != false {
			t.Fatalf("%s isError = %v", item.name, result["isError"])
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("%s 缺少 content：%v", item.name, result)
		}
		first := content[0].(map[string]any)
		if first["type"] != "text" {
			t.Fatalf("%s content[0].type = %v", item.name, first["type"])
		}
		if text, _ := first["text"].(string); !strings.Contains(text, item.want) {
			t.Fatalf("%s 返回 %q，未包含 %q", item.name, text, item.want)
		}
	}
}

// 工具提示不给出最终答案：这是"解题引导"类工具的硬性要求。
func TestFormulaHintNeverGivesTheAnswer(t *testing.T) {
	text := hintFor("一元二次方程")
	for _, forbidden := range []string{"答案", "x=", "得数"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("提示里出现了 %q：%s", forbidden, text)
		}
	}
}

// 不支持的换算也要能读懂：isError 为真，正文说明原因。
func TestUnsupportedConversionIsReported(t *testing.T) {
	text, ok := convert(1, "千克", "克")
	if ok {
		t.Fatal("不该声称换算成功")
	}
	if !strings.Contains(text, "只支持长度单位") {
		t.Fatalf("text = %q", text)
	}
}

// 三类 JSON-RPC 错误必须可区分，且响应中不含 result.content。
func TestJSONRPCErrors(t *testing.T) {
	handler := newMux(testToken)
	cases := []struct {
		name string
		body string
		code float64
	}{
		{"非法 JSON", `{"jsonrpc":"2.0","id":5,`, codeParseError},
		{"未知方法", `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`, codeMethodNotFound},
		{"缺参数", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"unit_convert","arguments":{"from":"米","to":"厘米"}}}`, codeInvalidParams},
		{"缺工具名", `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"arguments":{}}}`, codeInvalidParams},
	}
	for _, item := range cases {
		payload := decode(t, post(t, handler, testToken, item.body))
		failure, ok := payload["error"].(map[string]any)
		if !ok {
			t.Fatalf("%s 应返回 error：%v", item.name, payload)
		}
		if failure["code"] != item.code {
			t.Fatalf("%s code = %v, want %v", item.name, failure["code"], item.code)
		}
		if _, exists := payload["result"]; exists {
			t.Fatalf("%s 不应带 result", item.name)
		}
		if strings.Contains(recorderBody(payload), "content") {
			t.Fatalf("%s 的响应里出现了 content", item.name)
		}
	}
}

func recorderBody(payload map[string]any) string {
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}
