package mcp

import (
	"strings"
	"testing"
	"time"
)

// 一个都不配时的默认值：服务照常启动，工具循环按 10 秒 / 3 轮 / 单轮 4 次工作。
func TestFromEnvUsesDefaults(t *testing.T) {
	settings, err := FromEnv(func(string) string { return "" }, "test-secret")
	if err != nil {
		t.Fatalf("空配置不应报错：%v", err)
	}
	if settings.CallTimeout != DefaultCallTimeout {
		t.Fatalf("CallTimeout = %s, want %s", settings.CallTimeout, DefaultCallTimeout)
	}
	if settings.MaxRounds != DefaultMaxRounds || settings.MaxCalls != DefaultMaxCalls {
		t.Fatalf("配额 = %d/%d, want %d/%d", settings.MaxRounds, settings.MaxCalls, DefaultMaxRounds, DefaultMaxCalls)
	}
	if len(settings.TokenKey) != 32 {
		t.Fatalf("令牌密钥长度 = %d, want 32（AES-256）", len(settings.TokenKey))
	}
}

// 非法值只报告变量名，不回显变量值——这些变量里可能有密钥。
func TestFromEnvRejectsInvalidValues(t *testing.T) {
	for _, key := range []string{"MCP_CALL_TIMEOUT", "MCP_MAX_ROUNDS", "MCP_MAX_CALLS"} {
		getenv := func(name string) string {
			if name == key {
				return "abc"
			}
			return ""
		}
		_, err := FromEnv(getenv, "test-secret")
		if err == nil {
			t.Fatalf("%s=abc 应被拒绝", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("错误信息未指出变量名 %s：%v", key, err)
		}
		if strings.Contains(err.Error(), "abc") {
			t.Fatalf("错误信息回显了变量值：%v", err)
		}
	}
}

// 显式给值时按值生效，并且换 TOKEN_SECRET 会得到不同的加密密钥。
func TestFromEnvHonoursValues(t *testing.T) {
	values := map[string]string{"MCP_CALL_TIMEOUT": "3s", "MCP_MAX_ROUNDS": "1", "MCP_MAX_CALLS": "2"}
	settings, err := FromEnv(func(name string) string { return values[name] }, "secret-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settings.CallTimeout != 3*time.Second || settings.MaxRounds != 1 || settings.MaxCalls != 2 {
		t.Fatalf("配置未生效：%+v", settings)
	}
	other, err := FromEnv(func(name string) string { return values[name] }, "secret-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(settings.TokenKey) == string(other.TokenKey) {
		t.Fatal("不同的 TOKEN_SECRET 应派生不同的令牌密钥")
	}
}
