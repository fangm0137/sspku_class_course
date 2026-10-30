package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// 客户端错误分类。上层据此决定状态码与提示文案，不把上游原文透给浏览器。
var (
	ErrInvalidURL = errors.New("invalid mcp server url")
	ErrAuthFailed = errors.New("mcp server rejected the token")
	ErrUpstream   = errors.New("mcp server call failed")
)

const (
	rpcVersion    = "2.0"
	clientName    = "campusclaw"
	clientVersion = "1.0"
	// responseLimit 限制单次响应体大小，避免异常服务端把内存撑爆。
	responseLimit = 1 << 20
)

// Tool 是一次 tools/list 发现结果里的工具：三项全部来自远端，不做人工改写。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// CallResult 是一次 tools/call 的结果：文本数组 + 是否出错。
type CallResult struct {
	Content []string
	IsError bool
}

// Client 是对单台 MCP 服务器的 JSON-RPC 客户端。令牌只存在于此结构体内，
// 不进入日志、不进入任何响应。
type Client struct {
	endpoint string
	token    string
	http     *http.Client
	nextID   atomic.Int64
}

// NewClient 校验 Base URL 并构造客户端。URL 只允许 http/https，且不允许内嵌账号密码——
// 凭据必须走 Authorization 头，不能躺在数据库的一列里。
func NewClient(baseURL, token string, timeout time.Duration) (*Client, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" || len(trimmed) > 255 {
		return nil, ErrInvalidURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return nil, ErrInvalidURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, ErrInvalidURL
	}
	if parsed.User != nil {
		return nil, ErrInvalidURL
	}
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	return &Client{
		endpoint: endpointOf(parsed),
		token:    strings.TrimSpace(token),
		http:     &http.Client{Timeout: timeout},
	}, nil
}

// endpointOf 把 Base URL 归一为单端点 /mcp：教师填 http://mcp:8090 或
// http://mcp:8090/mcp 都指向同一个地方。
func endpointOf(parsed *url.URL) string {
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/mcp"
	} else if !strings.HasSuffix(path, "/mcp") {
		path += "/mcp"
	}
	return parsed.Scheme + "://" + parsed.Host + path
}

// Endpoint 便于排障时确认实际打到哪个地址（不含令牌）。
func (c *Client) Endpoint() string { return c.endpoint }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// Initialize 完成握手。返回值只用于确认对端确实是一个 MCP 服务。
func (c *Client) Initialize(ctx context.Context) (string, error) {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": clientName, "version": clientVersion},
	}
	var result struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return "", err
	}
	label := strings.TrimSpace(result.ServerInfo.Name + " " + result.ServerInfo.Version)
	return strings.TrimSpace(label), nil
}

// Discover 取回工具清单。清单整体替换由 Store 负责，本方法只做发现。
func (c *Client) Discover(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	tools := make([]Tool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		if len(tool.InputSchema) == 0 {
			tool.InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// Call 执行一次 tools/call。arguments 必须是 JSON 对象原文，原样转发。
func (c *Client) Call(ctx context.Context, name string, arguments json.RawMessage) (CallResult, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	params := map[string]any{
		"name":      name,
		"arguments": json.RawMessage(arguments),
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return CallResult{}, err
	}
	texts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if item.Type == "text" && item.Text != "" {
			texts = append(texts, item.Text)
		}
	}
	return CallResult{Content: texts, IsError: result.IsError}, nil
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	payload, err := json.Marshal(rpcRequest{JSONRPC: rpcVersion, ID: c.nextID.Add(1), Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%w: encode", ErrUpstream)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: build request", ErrUpstream)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	if err != nil {
		return fmt.Errorf("%w: read response", ErrUpstream)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrAuthFailed
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("%w: status %d", ErrUpstream, response.StatusCode)
	}
	var decoded rpcResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("%w: decode response", ErrUpstream)
	}
	if decoded.Error != nil {
		return fmt.Errorf("%w: %s", ErrUpstream, decoded.Error.Message)
	}
	if len(decoded.Result) == 0 {
		return fmt.Errorf("%w: empty result", ErrUpstream)
	}
	if err := json.Unmarshal(decoded.Result, out); err != nil {
		return fmt.Errorf("%w: decode result", ErrUpstream)
	}
	return nil
}
