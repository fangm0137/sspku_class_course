// Package mcp 实现"外部工具"这一侧的宿主职责：登记服务器、连接与发现、
// 把发现结果转成模型可见的工具声明、在授权通过后携带令牌调用远端工具。
//
// 边界：模型只提议调用，拿不到服务器地址与令牌；宿主按同一份 inputSchema 校验参数后才执行。
package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 默认值：没有配置任何变量时按这一组工作。
const (
	DefaultCallTimeout = 10 * time.Second
	DefaultMaxRounds   = 3
	DefaultMaxCalls    = 4

	// 上限的存在意义是防止一次提问被模型拖成无限循环。
	maxRoundsLimit = 10
	maxCallsLimit  = 20

	// deriveInfo 是子密钥派生的上下文串，与 JWT 密钥隔离。
	deriveInfo = "campusclaw/mcp-token/v1"
)

// Settings 是工具循环的三项配额与令牌加密密钥。全部可选：缺失即取默认值。
type Settings struct {
	CallTimeout time.Duration
	MaxRounds   int
	MaxCalls    int
	// TokenKey 是 AES-GCM 密钥（32 字节）。由 MCP_TOKEN_KEY 或 TOKEN_SECRET 派生而来。
	TokenKey []byte
}

// FromEnv 读取 MCP 配置。错误只指出变量名，不回显变量值——
// 这些变量里可能有密钥，配置错误的报错本身不该成为泄露面。
func FromEnv(getenv func(string) string, secret string) (Settings, error) {
	settings := Settings{
		CallTimeout: DefaultCallTimeout,
		MaxRounds:   DefaultMaxRounds,
		MaxCalls:    DefaultMaxCalls,
	}
	if raw := strings.TrimSpace(getenv("MCP_CALL_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 || parsed > 60*time.Second {
			return Settings{}, fmt.Errorf("invalid configuration: MCP_CALL_TIMEOUT must be a duration between 1s and 60s")
		}
		settings.CallTimeout = parsed
	}
	for _, item := range []struct {
		key    string
		target *int
		limit  int
	}{
		{"MCP_MAX_ROUNDS", &settings.MaxRounds, maxRoundsLimit},
		{"MCP_MAX_CALLS", &settings.MaxCalls, maxCallsLimit},
	} {
		raw := strings.TrimSpace(getenv(item.key))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > item.limit {
			return Settings{}, fmt.Errorf("invalid configuration: %s must be an integer between 1 and %d", item.key, item.limit)
		}
		*item.target = value
	}
	key := strings.TrimSpace(getenv("MCP_TOKEN_KEY"))
	if key == "" {
		// 缺省从 TOKEN_SECRET 派生：换 JWT 密钥会同时让已存的令牌解不开，
		// 这个代价在 MCP.md 里写明——重新填一次令牌即可。
		key = secret
	}
	settings.TokenKey = deriveKey(key)
	return settings, nil
}

// deriveKey 用 HMAC 派生子密钥，而不是直接拿 TOKEN_SECRET 当 AES 密钥：
// 同一份密钥材料在两个用途上不应互相牵连。
func deriveKey(material string) []byte {
	mac := hmac.New(sha256.New, []byte(material))
	mac.Write([]byte(deriveInfo))
	return mac.Sum(nil)
}
