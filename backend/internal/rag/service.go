package rag

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

const (
	// TopK 是交给对话网关的切片条数。
	TopK = 4
	// VectorThreshold 是向量候选的余弦相似度下限，低于它的候选直接丢弃。
	VectorThreshold = 0.35
	// NoEvidenceAnswer 是没有命中时使用的固定文案，此时不调用对话网关。
	NoEvidenceAnswer = "资料中未找到相关内容。"

	keywordLimit = 20
	vectorLimit  = 20
	embedBatch   = 16

	statusReady  = "ready"
	statusFailed = "failed"
)

// Config 是可选的外部依赖配置。任何一项缺失都只关闭对应能力，不让进程启动失败。
type Config struct {
	QdrantURL          string
	QdrantCollection   string
	EmbeddingBaseURL   string
	EmbeddingAPIKey    string
	EmbeddingModel     string
	EmbeddingDimension int
	ChatBaseURL        string
	ChatAPIKey         string
	ChatModel          string
}

// FromEnv 读取网关与向量库配置。这里的键名与课程网关给出的接入配置一致，
// 拿到课程网关的 EMBEDDING_* 后直接填进环境即可，不需要改代码。
func FromEnv(getenv func(string) string) Config {
	dimension, err := strconv.Atoi(strings.TrimSpace(getenv("EMBEDDING_DIM")))
	if err != nil || dimension <= 0 {
		dimension = 0
	}
	return Config{
		QdrantURL:          fallback(getenv("QDRANT_URL"), "http://qdrant:6333"),
		QdrantCollection:   fallback(getenv("QDRANT_COLLECTION"), "campusclaw_chunks"),
		EmbeddingBaseURL:   strings.TrimSpace(getenv("EMBEDDING_BASE_URL")),
		EmbeddingAPIKey:    strings.TrimSpace(getenv("EMBEDDING_API_KEY")),
		EmbeddingModel:     strings.TrimSpace(getenv("EMBEDDING_MODEL")),
		EmbeddingDimension: dimension,
		ChatBaseURL:        strings.TrimSpace(getenv("CHAT_BASE_URL")),
		ChatAPIKey:         strings.TrimSpace(getenv("CHAT_API_KEY")),
		ChatModel:          strings.TrimSpace(getenv("CHAT_MODEL")),
	}
}

func fallback(value, preset string) string {
	if strings.TrimSpace(value) == "" {
		return preset
	}
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

// ChunkRow 是切分后待写入或待嵌入的一个切片。
type ChunkRow struct {
	ID         uint64
	MaterialID uint64
	ClassID    uint64
	Index      int
	Text       string
}

// Hit 是一条可回溯的检索命中；Text 与 Title 都取自数据库，不采信模型复述。
type Hit struct {
	ChunkID     uint64
	MaterialID  uint64
	Title       string
	ChunkIndex  int
	StartOffset int
	EndOffset   int
	Text        string
}

// Service 把切片入库、检索与问答串起来。它可以在没有任何网关配置时构建：
// 此时向量路径关闭、问答返回依赖不可用，而关键字检索照常工作。
type Service struct {
	db        *sql.DB
	gateway   *Gateway
	vectors   *Qdrant
	dimension int
}

func New(db *sql.DB, config Config) *Service {
	return &Service{db: db, gateway: newGateway(config), vectors: newQdrant(config), dimension: config.EmbeddingDimension}
}

func (s *Service) VectorEnabled() bool {
	return s != nil && s.gateway.VectorEnabled() && s.vectors.Enabled()
}

func (s *Service) ChatEnabled() bool {
	return s != nil && s.gateway.ChatEnabled()
}

// IndexChunks 在事务之外为切片生成向量并写入 Qdrant。
// 失败只把切片标成 failed，材料与知识库记录保持有效；问答的向量路径只取 ready 的切片。
func (s *Service) IndexChunks(ctx context.Context, rows []ChunkRow) error {
	if s == nil || !s.VectorEnabled() || len(rows) == 0 {
		return nil
	}
	if err := s.vectors.Ensure(ctx, s.dimension); err != nil {
		s.markChunks(ctx, rows, statusFailed)
		return err
	}
	for start := 0; start < len(rows); start += embedBatch {
		end := start + embedBatch
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		texts := make([]string, len(batch))
		for index, row := range batch {
			texts[index] = row.Text
		}
		vectors, err := s.gateway.Embed(ctx, texts)
		if err != nil {
			s.markChunks(ctx, batch, statusFailed)
			continue
		}
		points := make([]vectorPoint, len(batch))
		for index, row := range batch {
			points[index] = vectorPoint{
				ID:     row.ID,
				Vector: vectors[index],
				Payload: map[string]any{
					"class_id":    row.ClassID,
					"material_id": row.MaterialID,
					"chunk_id":    row.ID,
					"chunk_index": row.Index,
				},
			}
		}
		if err := s.vectors.Upsert(ctx, points); err != nil {
			s.markChunks(ctx, batch, statusFailed)
			continue
		}
		s.markChunks(ctx, batch, statusReady)
	}
	return nil
}

func (s *Service) markChunks(ctx context.Context, rows []ChunkRow, status string) {
	if len(rows) == 0 {
		return
	}
	placeholders := make([]string, len(rows))
	args := make([]any, 0, len(rows)+1)
	args = append(args, status)
	for index, row := range rows {
		placeholders[index] = "?"
		args = append(args, row.ID)
	}
	// status 只来自本文件的常量，不是外部输入。
	_, _ = s.db.ExecContext(ctx, "UPDATE knowledge_chunks SET embed_status=? WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
}

// Search 按班级检索：关键字路径始终参与；向量路径在配置齐全时参与，两条都可用时做 RRF 融合。
func (s *Service) Search(ctx context.Context, classID uint64, question string, limit int) ([]Hit, error) {
	if s == nil || s.db == nil {
		return nil, ErrDependency
	}
	keyword, err := s.keyword(ctx, classID, question, keywordLimit)
	if err != nil {
		return nil, err
	}
	paths := [][]uint64{keyword}
	if s.VectorEnabled() {
		vector, vectorErr := s.vector(ctx, classID, question, vectorLimit)
		switch {
		case vectorErr == nil:
			paths = append(paths, vector)
		case len(keyword) == 0:
			// 关键字也没有候选时如实上报依赖故障，不把故障说成"资料里没有"。
			return nil, vectorErr
		}
	}
	return s.hydrate(ctx, classID, Fuse(paths, limit))
}

// Ask 先检索后生成：没有命中就直接说明资料里没有，并且不调用对话网关。
func (s *Service) Ask(ctx context.Context, classID uint64, question string) (string, []Hit, error) {
	hits, err := s.Search(ctx, classID, question, TopK)
	if err != nil {
		return "", nil, err
	}
	if len(hits) == 0 {
		return NoEvidenceAnswer, []Hit{}, nil
	}
	if !s.ChatEnabled() {
		return "", nil, ErrDependency
	}
	answer, err := s.gateway.Answer(ctx, question, hits)
	if err != nil {
		return "", nil, err
	}
	return answer, hits, nil
}

// keyword 用 MySQL 8.0 的 ngram 全文索引检索，班级条件写在 SQL 里，不依赖调用方过滤。
func (s *Service) keyword(ctx context.Context, classID uint64, question string, limit int) ([]uint64, error) {
	const query = `SELECT c.id
FROM knowledge_chunks c
WHERE c.class_id = ? AND MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE)
ORDER BY MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE) DESC, c.id ASC
LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, classID, question, question, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uint64, 0, limit)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) vector(ctx context.Context, classID uint64, question string, limit int) ([]uint64, error) {
	vectors, err := s.gateway.Embed(ctx, []string{question})
	if err != nil {
		return nil, err
	}
	points, err := s.vectors.Search(ctx, vectors[0], classID, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(points))
	for _, point := range points {
		// Qdrant 在 Cosine 距离下返回的是余弦相似度，越大越相似。
		if point.Score < VectorThreshold {
			continue
		}
		ids = append(ids, point.ID)
	}
	return ids, nil
}

// hydrate 用切片主键回表取正文与材料标题，并按融合后的顺序重排。
// 班级条件再次出现在 SQL 里：即使向量库返回了越界候选，也不会被带进回答。
func (s *Service) hydrate(ctx context.Context, classID uint64, ids []uint64) ([]Hit, error) {
	if len(ids) == 0 {
		return []Hit{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, classID)
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, id)
	}
	const query = `SELECT c.id, c.material_id, m.original_name, c.chunk_index, c.start_offset, c.end_offset, c.chunk_text
FROM knowledge_chunks c JOIN materials m ON m.id = c.material_id
WHERE c.class_id = ? AND c.id IN (`
	rows, err := s.db.QueryContext(ctx, query+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make(map[uint64]Hit, len(ids))
	for rows.Next() {
		var hit Hit
		if err := rows.Scan(&hit.ChunkID, &hit.MaterialID, &hit.Title, &hit.ChunkIndex, &hit.StartOffset, &hit.EndOffset, &hit.Text); err != nil {
			return nil, err
		}
		found[hit.ChunkID] = hit
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(ids))
	for _, id := range ids {
		if hit, ok := found[id]; ok {
			hits = append(hits, hit)
		}
	}
	return hits, nil
}

// ErrInvalidQuestion 由调用方在问题为空时返回 400 时使用。
var ErrInvalidQuestion = errors.New("invalid question")
