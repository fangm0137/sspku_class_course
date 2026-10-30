// Package main 是一个最小的 MCP 服务端实现：单端点 JSON-RPC 2.0，标准库，无外部依赖。
//
// 它存在的意义不是"功能齐全"，而是让课程项目的第五项能力（外部工具与 MCP 接入）
// 有一个同仓库、可复现、令牌不外泄的被调用方：宿主侧（backend）连它、发现它的工具、
// 带令牌调用它，整条链路都在这个仓库里可以被审计。
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// protocolVersion 写死一个版本：本服务只实现 initialize / tools/list / tools/call 三个方法。
const protocolVersion = "2024-11-05"

func main() {
	addr := os.Getenv("MCP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           newMux(os.Getenv("MCP_AUTH_TOKEN")),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Print("mcp server stopped")
		os.Exit(1)
	}
}

// newMux 组装两个端点。令牌为空时 /mcp 一律 401：
// 宁可服务不可用，也不允许"忘了配令牌"变成"谁都能调"。
func newMux(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// 存活探针不依赖鉴权，也不透露任何工具或配置信息。
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		handleRPC(w, r)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
