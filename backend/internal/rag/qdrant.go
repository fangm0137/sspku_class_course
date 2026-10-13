package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// vectorPoint 是写入 Qdrant 的点：主键与 knowledge_chunks.id 一致，
// payload 只放检索与回表需要的标识，正文始终以 MySQL 为准。
type vectorPoint struct {
	ID      uint64         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

// scoredPoint 是检索返回的一个候选，Score 在余弦距离下就是余弦相似度（越大越相似）。
type scoredPoint struct {
	ID    uint64
	Score float64
}

// Qdrant 是直接调用 REST 的最小客户端，不引入官方客户端库。
type Qdrant struct {
	base       string
	collection string
	client     *http.Client
}

func newQdrant(config Config) *Qdrant {
	return &Qdrant{
		base:       strings.TrimRight(config.QdrantURL, "/"),
		collection: config.QdrantCollection,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (q *Qdrant) Enabled() bool {
	return q != nil && q.base != "" && q.collection != ""
}

// Ensure 建立集合。已存在不算失败，因为每次启动都会调用。
func (q *Qdrant) Ensure(ctx context.Context, dimension int) error {
	body, err := json.Marshal(map[string]any{
		"vectors": map[string]any{"size": dimension, "distance": "Cosine"},
	})
	if err != nil {
		return fmt.Errorf("%w: encode collection", ErrDependency)
	}
	status, payload, err := q.do(ctx, http.MethodPut, "/collections/"+q.collection, body)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusConflict || bytes.Contains(payload, []byte("already exists")) {
		return nil
	}
	return fmt.Errorf("%w: ensure collection status %d", ErrDependency, status)
}

// Upsert 写入或覆盖切片向量，等待写入完成后再返回，保证紧接着的检索能看到数据。
func (q *Qdrant) Upsert(ctx context.Context, points []vectorPoint) error {
	if len(points) == 0 {
		return nil
	}
	body, err := json.Marshal(map[string]any{"points": points})
	if err != nil {
		return fmt.Errorf("%w: encode points", ErrDependency)
	}
	status, _, err := q.do(ctx, http.MethodPut, "/collections/"+q.collection+"/points?wait=true", body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("%w: upsert status %d", ErrDependency, status)
	}
	return nil
}

// Search 按班级过滤后做余弦检索；materialIDs 非空时进一步限定在这些材料内
// （智能体绑定的材料）。两个条件都写在 Qdrant 的 filter 里，与 MySQL 侧互为双保险。
func (q *Qdrant) Search(ctx context.Context, vector []float32, classID uint64, materialIDs []uint64, limit int) ([]scoredPoint, error) {
	must := []map[string]any{{"key": "class_id", "match": map[string]any{"value": classID}}}
	if len(materialIDs) > 0 {
		scope := make([]uint64, len(materialIDs))
		copy(scope, materialIDs)
		must = append(must, map[string]any{"key": "material_id", "match": map[string]any{"any": scope}})
	}
	body, err := json.Marshal(map[string]any{
		"query":        vector,
		"limit":        limit,
		"with_payload": false,
		"filter":       map[string]any{"must": must},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode query", ErrDependency)
	}
	status, payload, err := q.do(ctx, http.MethodPost, "/collections/"+q.collection+"/points/query", body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%w: query status %d", ErrDependency, status)
	}
	var reply struct {
		Result struct {
			Points []struct {
				ID    json.Number `json:"id"`
				Score float64     `json:"score"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		return nil, fmt.Errorf("%w: decode query", ErrDependency)
	}
	points := make([]scoredPoint, 0, len(reply.Result.Points))
	for _, item := range reply.Result.Points {
		id, err := item.ID.Int64()
		if err != nil || id <= 0 {
			continue
		}
		points = append(points, scoredPoint{ID: uint64(id), Score: item.Score})
	}
	return points, nil
}

// DeleteByMaterial 删除某份材料的全部向量，用于显式重建索引前的清理。
// 用 payload 过滤器而不是点 id 列表，避免重建时漏删不再存在的旧切片。
func (q *Qdrant) DeleteByMaterial(ctx context.Context, materialID uint64) error {
	body, err := json.Marshal(map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{{"key": "material_id", "match": map[string]any{"value": materialID}}},
		},
	})
	if err != nil {
		return fmt.Errorf("%w: encode delete", ErrDependency)
	}
	status, _, err := q.do(ctx, http.MethodPost, "/collections/"+q.collection+"/points/delete?wait=true", body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("%w: delete status %d", ErrDependency, status)
	}
	return nil
}

func (q *Qdrant) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, q.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: build request", ErrDependency)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := q.client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrDependency, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: read response", ErrDependency)
	}
	return response.StatusCode, payload, nil
}
