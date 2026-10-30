package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// JSON-RPC 2.0 的错误码。这三类都要能被客户端区分，不以 200 加空结果冒充成功。
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

const requestLimit = 1 << 20

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// authorized 用恒定时间比较，避免从响应时间上试探令牌。
func authorized(r *http.Request, token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	presented := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

func handleRPC(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, requestLimit))
	if err != nil {
		writeJSON(w, http.StatusOK, failure(nil, codeParseError, "读取请求体失败"))
		return
	}
	var request rpcRequest
	if err := json.Unmarshal(body, &request); err != nil {
		// 非法 JSON 是解析错误，不是"方法不存在"。
		writeJSON(w, http.StatusOK, failure(nil, codeParseError, "请求不是合法的 JSON"))
		return
	}
	if request.Method == "" {
		writeJSON(w, http.StatusOK, failure(request.ID, codeInvalidRequest, "缺少 method"))
		return
	}
	switch request.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, success(request.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "campusclaw-mcp", "version": "1.0.0"},
		}))
	case "tools/list":
		writeJSON(w, http.StatusOK, success(request.ID, map[string]any{"tools": toolCatalog()}))
	case "tools/call":
		result, rpcErr := callTool(request.Params)
		if rpcErr != nil {
			writeJSON(w, http.StatusOK, failure(request.ID, rpcErr.Code, rpcErr.Message))
			return
		}
		writeJSON(w, http.StatusOK, success(request.ID, result))
	case "ping":
		writeJSON(w, http.StatusOK, success(request.ID, map[string]any{}))
	default:
		writeJSON(w, http.StatusOK, failure(request.ID, codeMethodNotFound, "不支持的方法："+request.Method))
	}
}

func success(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func failure(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}
