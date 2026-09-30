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

	// SearchDefaultLimit 与 SearchMaxLimit 限定一次检索返回的命中条数。
	SearchDefaultLimit = 4
	SearchMaxLimit     = 20

	keywordLimit = 20
	vectorLimit  = 20
	embedBatch   = 16

	// 切片索引状态。ready 表示可被检索；pending 表示正文已入库、向量尚未写入；
	// failed 表示建索引失败（材料与正文仍在，关键字检索不会命中该切片）。
	StatusPending = "pending"
	StatusReady   = "ready"
	StatusFailed  = "failed"

	// ModeKeyword 只用 MySQL ngram 全文索引，不调用嵌入网关、不访问向量库。
	ModeKeyword = "keyword"
	// ModeVector 先把问句嵌入再在向量库按班级过滤。
	ModeVector = "vector"
	// ModeHybrid 同时执行两条路径并按名次做 RRF 融合，未指定时使用。
	ModeHybrid = "hybrid"
)

// ErrInvalidMode 表示请求里的检索模式非法，调用方应返回 400。
var ErrInvalidMode = errors.New("invalid search mode")

// ErrMaterialNotFound 表示该材料在本班没有可检索的正文，调用方应返回 404。
var ErrMaterialNotFound = errors.New("material knowledge entry not found")

// NormalizeMode 把空字符串归一为默认模式 hybrid。
func NormalizeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ModeHybrid
	}
	return mode
}

// ValidMode 判断归一化后的模式是否受支持。
func ValidMode(mode string) bool {
	switch mode {
	case ModeKeyword, ModeVector, ModeHybrid:
		return true
	}
	return false
}

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

// ChunkRow 是切分后待嵌入的一个切片。
type ChunkRow struct {
	ID         uint64
	MaterialID uint64
	ClassID    uint64
	EntryID    uint64
	Index      int
	Text       string
}

// Hit 是一条可回溯的检索命中；Text 与 Title 都取自关系库，不采信模型复述。
type Hit struct {
	ChunkID     uint64
	MaterialID  uint64
	Title       string
	ChunkIndex  int
	StartOffset int
	EndOffset   int
	Text        string
	Score       float64
}

// Service 把切片入库、检索与问答串起来。它可以在没有任何网关配置时构建：
// 此时向量路径关闭、hybrid 与问答返回依赖不可用，而关键字检索照常工作。
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

// EnsureCollection 按配置维度建立集合；Qdrant 不可用时只返回错误，进程继续运行。
func (s *Service) EnsureCollection(ctx context.Context) error {
	if s == nil || !s.VectorEnabled() {
		return nil
	}
	return s.vectors.Ensure(ctx, s.dimension)
}

// ChunkInsert 描述一条待写入的切片。
type ChunkInsert struct {
	Index    int
	Start    int
	End      int
	Text     string
	Strategy string
	Status   string
}

// InsertChunks 在调用方的事务里写入切片，返回带主键的行供后续嵌入使用。
// 材料、正文与切片同事务提交，任一失败都不会留下半成品。
func InsertChunks(ctx context.Context, tx *sql.Tx, materialID, classID, entryID uint64, inserts []ChunkInsert) ([]ChunkRow, error) {
	rows := make([]ChunkRow, 0, len(inserts))
	for _, insert := range inserts {
		status := insert.Status
		if status == "" {
			status = StatusPending
		}
		result, err := tx.ExecContext(ctx,
			`INSERT INTO knowledge_chunks
			 (material_id,class_id,knowledge_entry_id,chunk_index,char_start,char_end,chunk_text,strategy,index_status)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			materialID, classID, entryID, insert.Index, insert.Start, insert.End, insert.Text, insert.Strategy, status)
		if err != nil {
			return nil, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		rows = append(rows, ChunkRow{
			ID: uint64(id), MaterialID: materialID, ClassID: classID, EntryID: entryID,
			Index: insert.Index, Text: insert.Text,
		})
	}
	return rows, nil
}

// IndexChunks 在事务之外为切片生成向量并写入 Qdrant。
// 失败只把切片标成 failed，材料与知识库记录保持有效；检索只取 ready 的切片。
func (s *Service) IndexChunks(ctx context.Context, rows []ChunkRow) error {
	if s == nil || !s.VectorEnabled() || len(rows) == 0 {
		return nil
	}
	if err := s.vectors.Ensure(ctx, s.dimension); err != nil {
		s.markChunks(ctx, rows, StatusFailed)
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
			s.markChunks(ctx, batch, StatusFailed)
			continue
		}
		points := make([]vectorPoint, len(batch))
		for index, row := range batch {
			points[index] = vectorPoint{
				ID:     row.ID,
				Vector: vectors[index],
				// payload 只放标识：正文始终以 MySQL knowledge_chunks.chunk_text 为准。
				Payload: map[string]any{
					"class_id":           row.ClassID,
					"material_id":        row.MaterialID,
					"knowledge_entry_id": row.EntryID,
					"chunk_id":           row.ID,
					"chunk_index":        row.Index,
				},
			}
		}
		if err := s.vectors.Upsert(ctx, points); err != nil {
			s.markChunks(ctx, batch, StatusFailed)
			continue
		}
		s.markChunks(ctx, batch, StatusReady)
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
	_, _ = s.db.ExecContext(ctx, "UPDATE knowledge_chunks SET index_status=? WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
}

// RebuildIndex 是显式重建：先删除该材料的旧切片与旧向量，再按本次请求的策略重新写入。
// 返回新切片数量；失败不自动重试，也不回滚成旧索引（旧切片已按约定删除）。
func (s *Service) RebuildIndex(ctx context.Context, materialID, classID uint64, options SplitOptions) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrDependency
	}
	if err := options.Validate(); err != nil {
		return 0, err
	}
	var entryID uint64
	var body string
	err := s.db.QueryRowContext(ctx, "SELECT id, extracted_text FROM knowledge_entries WHERE material_id=? AND class_id=?", materialID, classID).Scan(&entryID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrMaterialNotFound
	}
	if err != nil {
		return 0, err
	}
	_, chunks, err := SplitWith(body, options)
	if err != nil {
		return 0, err
	}
	status := StatusPending
	if !s.VectorEnabled() {
		// 没有向量库时关键字索引在写入切片后即就绪。
		status = StatusReady
	}
	inserts := make([]ChunkInsert, len(chunks))
	for index, chunk := range chunks {
		inserts[index] = ChunkInsert{Index: chunk.Index, Start: chunk.Start, End: chunk.End, Text: chunk.Text, Strategy: options.EffectiveMode(), Status: status}
	}
	// 先删旧向量再删旧切片：避免重建中断后留下"有向量、无切片"的孤儿。
	if s.VectorEnabled() {
		if err := s.vectors.DeleteByMaterial(ctx, materialID); err != nil {
			return 0, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_chunks WHERE material_id=? AND class_id=?", materialID, classID); err != nil {
		return 0, err
	}
	rows, err := InsertChunks(ctx, tx, materialID, classID, entryID, inserts)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if s.VectorEnabled() {
		if err := s.IndexChunks(ctx, rows); err != nil {
			// 材料与切片仍在，只是标记为 failed；调用方据此提示可稍后重试。
			return len(chunks), err
		}
	}
	return len(chunks), nil
}

// SearchMode 是检索接口使用的严格语义：keyword 只走全文索引；vector 需要嵌入网关
// 与向量库；hybrid 同时执行两条路径并按名次融合。任一路径的依赖故障都如实上报 503，
// 不把故障说成"资料里没有"，也不用单侧结果冒充混合结果。
func (s *Service) SearchMode(ctx context.Context, classID uint64, question, mode string, limit int) ([]Hit, error) {
	return s.search(ctx, classID, question, mode, limit, true)
}

// search 是检索与问答共用的实现。strict 为 true 时任何在用路径的依赖故障都上报 503；
// 为 false 时按问答规约降级：向量路径未配置即自动停用，配置了但故障时只要关键字
// 已有候选就继续据此作答，只有两条路径都拿不到候选时才上报依赖不可用。
func (s *Service) search(ctx context.Context, classID uint64, question, mode string, limit int, strict bool) ([]Hit, error) {
	if s == nil || s.db == nil {
		return nil, ErrDependency
	}
	mode = NormalizeMode(mode)
	if !ValidMode(mode) {
		return nil, ErrInvalidMode
	}
	if limit <= 0 || limit > SearchMaxLimit {
		limit = SearchDefaultLimit
	}
	paths := make([][]Candidate, 0, 2)
	keywordHits := 0
	if mode == ModeKeyword || mode == ModeHybrid {
		keyword, err := s.keyword(ctx, classID, question, keywordLimit)
		if err != nil {
			return nil, err
		}
		keywordHits = len(keyword)
		paths = append(paths, keyword)
	}
	if mode == ModeVector || mode == ModeHybrid {
		vector, err := s.vectorPath(ctx, classID, question, strict, keywordHits > 0)
		if err != nil {
			return nil, err
		}
		if vector != nil {
			paths = append(paths, vector)
		}
	}
	var ordered []Candidate
	switch len(paths) {
	case 0:
		ordered = nil
	case 1:
		ordered = paths[0]
		if len(ordered) > limit {
			ordered = ordered[:limit]
		}
	default:
		ordered = FuseScored(paths, limit)
	}
	return s.hydrate(ctx, classID, ordered)
}

// vectorPath 返回 nil, nil 表示该路径被降级规则跳过（不是故障）。
func (s *Service) vectorPath(ctx context.Context, classID uint64, question string, strict, keywordHasHits bool) ([]Candidate, error) {
	if !s.VectorEnabled() {
		if strict {
			return nil, ErrDependency
		}
		return nil, nil
	}
	candidates, err := s.vector(ctx, classID, question, vectorLimit)
	if err != nil {
		if strict || !keywordHasHits {
			return nil, err
		}
		return nil, nil
	}
	return candidates, nil
}

// Ask 先检索后生成：没有命中就直接说明资料里没有，并且不调用对话网关。
func (s *Service) Ask(ctx context.Context, classID uint64, question string) (string, []Hit, error) {
	hits, err := s.search(ctx, classID, question, ModeHybrid, TopK, false)
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

// keyword 用 MySQL 8.0 的 ngram 全文索引检索。班级条件与索引状态都写在 SQL 里，
// 因此这条路径不依赖向量库，也不需要调用任何模型网关。
func (s *Service) keyword(ctx context.Context, classID uint64, question string, limit int) ([]Candidate, error) {
	const query = `SELECT c.id, MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE) AS relevance
FROM knowledge_chunks c
WHERE c.class_id = ? AND c.index_status = 'ready'
  AND MATCH(c.chunk_text) AGAINST (? IN NATURAL LANGUAGE MODE) > 0
ORDER BY relevance DESC, c.id ASC
LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, question, classID, question, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make([]Candidate, 0, limit)
	for rows.Next() {
		var candidate Candidate
		if err := rows.Scan(&candidate.ID, &candidate.Score); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (s *Service) vector(ctx context.Context, classID uint64, question string, limit int) ([]Candidate, error) {
	vectors, err := s.gateway.Embed(ctx, []string{question})
	if err != nil {
		return nil, err
	}
	points, err := s.vectors.Search(ctx, vectors[0], classID, limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(points))
	for _, point := range points {
		// Qdrant 在 Cosine 距离下返回的是余弦相似度，越大越相似。
		if point.Score < VectorThreshold {
			continue
		}
		candidates = append(candidates, Candidate{ID: point.ID, Score: point.Score})
	}
	return candidates, nil
}

// hydrate 用切片主键回表取正文与材料标题，并按融合后的顺序重排。
// 班级与 ready 条件再次出现在 SQL 里：即使向量库返回了越界候选，也不会被带进回答。
func (s *Service) hydrate(ctx context.Context, classID uint64, candidates []Candidate) ([]Hit, error) {
	if len(candidates) == 0 {
		return []Hit{}, nil
	}
	placeholders := make([]string, len(candidates))
	args := make([]any, 0, len(candidates)+1)
	args = append(args, classID)
	for index, candidate := range candidates {
		placeholders[index] = "?"
		args = append(args, candidate.ID)
	}
	const query = `SELECT c.id, c.material_id, m.original_name, c.chunk_index, c.char_start, c.char_end, c.chunk_text
FROM knowledge_chunks c JOIN materials m ON m.id = c.material_id
WHERE c.class_id = ? AND c.index_status = 'ready' AND c.id IN (`
	rows, err := s.db.QueryContext(ctx, query+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make(map[uint64]Hit, len(candidates))
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
	hits := make([]Hit, 0, len(candidates))
	for _, candidate := range candidates {
		if hit, ok := found[candidate.ID]; ok {
			hit.Score = candidate.Score
			hits = append(hits, hit)
		}
	}
	return hits, nil
}
