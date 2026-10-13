package httpapi

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
	"strconv"
)

// openAPIPath 是接口文档自身的路径。它公开可读：文档只描述形状，
// 不含任何密钥、班级数据或提示词。
const openAPIPath = "/openapi.yaml"

// openAPIDocument 是仓库内的接口文档。它与 handler.go 的 routeTable 由
// openapi_test.go 逐条比对，避免文档与代码各自漂移。
//
//go:embed openapi.yaml
var openAPIDocument []byte

// openAPIDigest 是文档内容摘要，作为 ETag 便于 Swagger 页面复用缓存。
var openAPIDigest = func() string {
	sum := sha256.Sum256(openAPIDocument)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}()

// serveOpenAPI 输出 OpenAPI 文档。ETag 命中时返回 304，浏览器刷新文档不必重传。
func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", openAPIDigest)
	w.Header().Set("Content-Length", strconv.Itoa(len(openAPIDocument)))
	if match := r.Header.Get("If-None-Match"); match == openAPIDigest {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPIDocument)
}
