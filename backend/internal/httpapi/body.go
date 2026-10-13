package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"
)

const jsonBodyLimit = 64 * 1024

// decodeJSON 读取单个 JSON 对象：限长、限 UTF-8、拒绝未知字段与尾随内容。
// 与登录、提问等接口保持一致的手写严格解析，避免解析歧义。
func decodeJSON(r *http.Request, limit int64, target any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(body)) > limit || !utf8.Valid(body) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return false
	}
	return true
}

// identityOf 取出中间件注入的账号行；只有经过 RequireIdentity 的处理器才会调用它。
func identityOf(r *http.Request) userIdentity {
	return r.Context().Value(identityKey{}).(userIdentity)
}
