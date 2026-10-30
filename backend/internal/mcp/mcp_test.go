package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// 引用格式：只允许一个 @，两侧都不能为空，不能有空白。
func TestParseRef(t *testing.T) {
	good := map[string]Ref{
		"formula_hint@campus-tools": {Tool: "formula_hint", Server: "campus-tools"},
		"  unit_convert@tools  ":    {Tool: "unit_convert", Server: "tools"},
	}
	for raw, want := range good {
		got, err := ParseRef(raw)
		if err != nil {
			t.Fatalf("ParseRef(%q) = %v, want ok", raw, err)
		}
		if got != want {
			t.Fatalf("ParseRef(%q) = %+v, want %+v", raw, got, want)
		}
	}
	bad := []string{"", "formula_hint", "a@b@c", "@server", "tool@", "to ol@server"}
	for _, raw := range bad {
		_, err := ParseRef(raw)
		if err == nil {
			t.Fatalf("ParseRef(%q) should fail", raw)
		}
		var refError *RefError
		if !asRefError(err, &refError) {
			t.Fatalf("ParseRef(%q) error type = %T", raw, err)
		}
		if refError.Category != CategoryFormat {
			t.Fatalf("category = %q, want %q", refError.Category, CategoryFormat)
		}
	}
}

func asRefError(err error, target **RefError) bool {
	value, ok := err.(*RefError)
	if ok {
		*target = value
	}
	return ok
}

// 参数校验：必填、类型、枚举与数组元素都要拦住；合法参数照常放行。
func TestValidateArguments(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"value":{"type":"number"},
			"from":{"type":"string"},
			"to":{"type":"string"},
			"digits":{"type":"integer"},
			"mode":{"type":"string","enum":["short","long"]},
			"tags":{"type":"array","items":{"type":"string"}}
		},
		"required":["value","from","to"]
	}`)
	valid := []string{
		`{"value":3.2,"from":"公里","to":"米"}`,
		`{"value":1,"from":"a","to":"b","digits":2,"mode":"short","tags":["x","y"]}`,
		`{"value":1.5,"from":"a","to":"b","mode":"long"}`,
	}
	for _, args := range valid {
		if err := ValidateArguments(schema, json.RawMessage(args)); err != nil {
			t.Fatalf("%s 应通过校验：%v", args, err)
		}
	}
	invalid := []struct {
		args    string
		mention string
	}{
		{`{"from":"a","to":"b"}`, "缺少必填字段 value"},
		{`{"value":"3.2","from":"a","to":"b"}`, "应为 number"},
		{`{"value":1,"from":"a","to":"b","mode":"middle"}`, "取值不在允许范围内"},
		{`{"value":1,"from":"a","to":"b","digits":1.5}`, "应为 integer"},
		{`{"value":1,"from":"a","to":"b","tags":[1]}`, "tags[0]"},
		{`[]`, "应为 JSON 对象"},
		{`{"value":1,"from":"a","to":"b"`, "不是合法的 JSON"},
	}
	for _, item := range invalid {
		err := ValidateArguments(schema, json.RawMessage(item.args))
		if err == nil {
			t.Fatalf("%s 应被拒绝", item.args)
		}
		if !strings.Contains(err.Error(), item.mention) {
			t.Fatalf("错误 %q 未指出 %q", err.Error(), item.mention)
		}
	}
}

// Base URL 校验：只允许 http/https，且不允许内嵌账号密码。
func TestNewClientValidatesURL(t *testing.T) {
	if _, err := NewClient("https://example.com", "t", 0); err != nil {
		t.Fatalf("https 应通过：%v", err)
	}
	client, err := NewClient("http://mcp:8090/mcp", "", 0)
	if err != nil {
		t.Fatalf("带 /mcp 的地址应通过：%v", err)
	}
	if client.Endpoint() != "http://mcp:8090/mcp" {
		t.Fatalf("endpoint = %q，不应重复拼接", client.Endpoint())
	}
	base, err := NewClient("http://mcp:8090", "", 0)
	if err != nil || base.Endpoint() != "http://mcp:8090/mcp" {
		t.Fatalf("endpoint = %q, err = %v", base.Endpoint(), err)
	}
	for _, raw := range []string{"", "ftp://x", "https://user:pass@x", "not-a-url", "://x"} {
		if _, err := NewClient(raw, "", 0); err == nil {
			t.Fatalf("%q 应被拒绝", raw)
		}
	}
}
