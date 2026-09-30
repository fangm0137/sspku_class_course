package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"campusclaw/backend/internal/rag"
)

const (
	searchQueryLimit = 500
	reindexBodyLimit = 4 * 1024
)

type searchHandler struct {
	service *rag.Service
}

// searchHit 的字段与 design 的接口表一致：命中必须能回溯到材料、切片序号与字符区间，
// excerpt 取自关系库的 chunk_text，score 只是本次模式的排序分。
type searchHit struct {
	MaterialID uint64  `json:"material_id"`
	Title      string  `json:"title"`
	ChunkID    uint64  `json:"chunk_id"`
	ChunkIndex int     `json:"chunk_index"`
	CharStart  int     `json:"char_start"`
	CharEnd    int     `json:"char_end"`
	Excerpt    string  `json:"excerpt"`
	Score      float64 `json:"score"`
}

type searchReply struct {
	Mode string      `json:"mode"`
	Hits []searchHit `json:"hits"`
}

// search 只从会话取班级：查询串里即使写了 class_id 也不会被读取。
func (h searchHandler) search(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(identityKey{}).(userIdentity)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || utf8.RuneCountInString(query) > searchQueryLimit {
		apiError(w, 400, "invalid_request", "查询内容不能为空")
		return
	}
	mode := rag.NormalizeMode(r.URL.Query().Get("mode"))
	if !rag.ValidMode(mode) {
		apiError(w, 400, "invalid_request", "检索模式无效")
		return
	}
	if h.service == nil {
		securityError(w, 503)
		return
	}
	limit := rag.SearchDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= rag.SearchMaxLimit {
			limit = value
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	hits, err := h.service.SearchMode(ctx, user.ClassID, query, mode, limit)
	if err != nil {
		writeRetrievalError(w, err)
		return
	}
	reply := searchReply{Mode: mode, Hits: make([]searchHit, 0, len(hits))}
	for _, hit := range hits {
		reply.Hits = append(reply.Hits, searchHit{
			MaterialID: hit.MaterialID,
			Title:      displayName(hit.Title),
			ChunkID:    hit.ChunkID,
			ChunkIndex: hit.ChunkIndex,
			CharStart:  hit.StartOffset,
			CharEnd:    hit.EndOffset,
			Excerpt:    hit.Text,
			Score:      roundScore(hit.Score),
		})
	}
	jsonReply(w, 200, reply)
}

type reindexHandler struct {
	service *rag.Service
}

// reindexRequest 是显式重建索引的参数。未给出的字段保持默认：
// 策略空值等同 auto；三个预处理开关默认关闭。
type reindexRequest struct {
	Strategy           string `json:"strategy"`
	Size               int    `json:"size"`
	Overlap            int    `json:"overlap"`
	RemoveURLs         bool   `json:"remove_urls"`
	RemoveEmails       bool   `json:"remove_emails"`
	CollapseWhitespace bool   `json:"collapse_whitespace"`
}

// parseReindex 只接受单个 JSON 对象，拒绝未知字段与重复字段，规则与登录、提问一致。
func parseReindex(r *http.Request) (reindexRequest, bool) {
	var request reindexRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, reindexBodyLimit+1))
	if err != nil || len(body) > reindexBodyLimit || !utf8.Valid(body) {
		return request, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return request, true
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return reindexRequest{}, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return reindexRequest{}, false
	}
	return request, true
}

// reindex 供教师按新策略重建本班某份材料的索引；跨班材料沿用 404，不泄露归属。
func (h reindexHandler) reindex(w http.ResponseWriter, r *http.Request) {
	id, valid := materialID(r)
	if !valid {
		materialNotFound(w)
		return
	}
	user := r.Context().Value(identityKey{}).(userIdentity)
	request, ok := parseReindex(r)
	if !ok {
		apiError(w, 400, "invalid_request", "请求无效")
		return
	}
	options := rag.SplitOptions{
		Mode:               request.Strategy,
		Size:               request.Size,
		Overlap:            request.Overlap,
		RemoveURLs:         request.RemoveURLs,
		RemoveEmails:       request.RemoveEmails,
		CollapseWhitespace: request.CollapseWhitespace,
	}
	if err := options.Validate(); err != nil {
		apiError(w, 400, "invalid_request", "切分参数无效")
		return
	}
	if h.service == nil {
		securityError(w, 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	count, err := h.service.RebuildIndex(ctx, id, user.ClassID, options)
	if err != nil {
		writeRetrievalError(w, err)
		return
	}
	jsonReply(w, 200, map[string]any{
		"material_id": id,
		"strategy":    options.EffectiveMode(),
		"chunks":      count,
	})
}

// writeRetrievalError 把检索模块的错误映射成接口状态码：
// 参数错误 400、材料不可见 404，其余依赖或数据库故障一律 503。
func writeRetrievalError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, rag.ErrInvalidMode), errors.Is(err, rag.ErrInvalidSplit):
		apiError(w, 400, "invalid_request", "请求无效")
	case errors.Is(err, rag.ErrMaterialNotFound):
		materialNotFound(w)
	default:
		securityError(w, 503)
	}
}

// roundScore 只做展示层面的取整，避免把浮点噪声写进响应。
func roundScore(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}
