package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"campusclaw/backend/internal/rag"
)

const askBodyLimit = 16 * 1024
const askQuestionLimit = 500

// askHandler 把本班检索与对话网关串起来。service 为 nil 时按依赖不可用处理。
type askHandler struct {
	service *rag.Service
}

type askCitation struct {
	MaterialID  uint64 `json:"material_id"`
	Title       string `json:"title"`
	ChunkIndex  int    `json:"chunk_index"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	Excerpt     string `json:"excerpt"`
	Citation    int    `json:"citation"`
}

type askReply struct {
	Answer string        `json:"answer"`
	Hits   []askCitation `json:"hits"`
}

// parseAsk 只接受单个 JSON 对象与唯一的 question 字段，规则与登录一致。
func parseAsk(r *http.Request) (string, bool) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, askBodyLimit+1))
	if err != nil || len(body) > askBodyLimit || !utf8.Valid(body) {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", false
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", false
		}
		name, ok := key.(string)
		if !ok {
			return "", false
		}
		if _, exists := fields[name]; exists {
			return "", false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return "", false
		}
		fields[name] = value
	}
	if _, err = decoder.Token(); err != nil {
		return "", false
	}
	if _, err = decoder.Token(); err != io.EOF {
		return "", false
	}
	var question string
	if json.Unmarshal(fields["question"], &question) != nil {
		return "", false
	}
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > askQuestionLimit {
		return "", false
	}
	return question, true
}

// ask 只从会话取班级：请求体里没有任何班级或角色字段可用。
func (a askHandler) ask(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(identityKey{}).(userIdentity)
	question, ok := parseAsk(r)
	if !ok {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	if a.service == nil {
		securityError(w, 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	answer, hits, err := a.service.Ask(ctx, user.ClassID, question)
	if errors.Is(err, rag.ErrDependency) {
		securityError(w, 503)
		return
	}
	if err != nil {
		securityError(w, 503)
		return
	}
	reply := askReply{Answer: answer, Hits: make([]askCitation, 0, len(hits))}
	for index, hit := range hits {
		reply.Hits = append(reply.Hits, askCitation{
			MaterialID:  hit.MaterialID,
			Title:       displayName(hit.Title),
			ChunkIndex:  hit.ChunkIndex,
			StartOffset: hit.StartOffset,
			EndOffset:   hit.EndOffset,
			Excerpt:     hit.Text,
			Citation:    index + 1,
		})
	}
	jsonReply(w, 200, reply)
}
