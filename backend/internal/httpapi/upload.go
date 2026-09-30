package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"campusclaw/backend/internal/rag"

	"github.com/go-sql-driver/mysql"
)

// Hooks are assigned only by tests, never by requests or configuration.
type uploadStore struct {
	db        *sql.DB
	directory string
	ready     atomic.Bool
	mu        sync.Mutex
	fault     func(string) error
	commit    func(*sql.Tx) error
	// rag 为 nil 时不做切片嵌入：未配置模型网关的环境仍能正常上传。
	rag *rag.Service
}

func newUploadStore(db *sql.DB, directory string) *uploadStore {
	s := &uploadStore{db: db, directory: directory}
	s.ready.Store(true)
	return s
}
func (s *uploadStore) step(name string) error {
	if s.fault != nil {
		return s.fault(name)
	}
	return nil
}
func (s *uploadStore) staging() string { return filepath.Join(filepath.Dir(s.directory), "staging") }

func (s *uploadStore) upload(w http.ResponseWriter, r *http.Request) {
	input, status := parseUpload(w, r)
	if status != 0 {
		if status == 413 {
			apiError(w, 413, "payload_too_large", "文件超过大小限制")
		} else {
			apiError(w, 400, "invalid_request", "请求无效")
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready.Load() {
		securityError(w, 503)
		return
	}
	if err := s.prepareDirectories(); err != nil {
		materialBroken(w)
		return
	}
	_, random := randomValue()
	temporary := "pending-" + random
	stage, err := os.OpenRoot(s.staging())
	if err != nil {
		materialBroken(w)
		return
	}
	defer stage.Close()
	target, err := os.OpenRoot(s.directory)
	if err != nil {
		materialBroken(w)
		return
	}
	defer target.Close()
	final := ""
	created := false
	cleanup := func() {
		if !created {
			return
		}
		var err error
		if err = s.step("cleanup"); err == nil {
			if final != "" {
				err = target.Remove(final)
			} else {
				err = stage.Remove(temporary)
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.ready.Store(false)
			log.Print("file compensation failed; repair storage and restart")
		}
	}
	if s.step("file") != nil {
		materialBroken(w)
		return
	}
	file, err := stage.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		materialBroken(w)
		return
	}
	created = true
	_, err = file.Write(input.body)
	if err == nil {
		err = s.step("file_written")
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		materialBroken(w)
		return
	}
	if s.step("extract") != nil {
		cleanup()
		materialBroken(w)
		return
	}
	user := r.Context().Value(identityKey{}).(userIdentity)
	// 切分是纯计算，放在事务之前：参数或正文不合法时直接失败，不开启事务。
	// 上传固定使用 auto 策略（规约：未指定策略即 auto），改变策略必须走显式重建索引。
	splitOptions := rag.SplitOptions{Mode: rag.ModeAuto}
	_, chunks, err := rag.SplitWith(input.text, splitOptions)
	if err != nil {
		cleanup()
		materialBroken(w)
		return
	}
	// 配了向量库时先落 pending，等嵌入成功再转 ready；没有向量库时关键字索引写入即就绪。
	chunkStatus := rag.StatusPending
	if s.rag == nil || !s.rag.VectorEnabled() {
		chunkStatus = rag.StatusReady
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		cleanup()
		securityError(w, 503)
		return
	}
	defer tx.Rollback()
	abort := func(status int) {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			s.ready.Store(false)
			securityError(w, 503)
			return
		}
		cleanup()
		if status == 503 {
			securityError(w, 503)
		} else {
			materialBroken(w)
		}
	}
	if s.step("material") != nil {
		abort(503)
		return
	}
	result, err := tx.ExecContext(r.Context(), "INSERT INTO materials (class_id,uploader_id,original_name,storage_key,size,source) VALUES (?,?,?,?,?,'upload')", user.ClassID, user.ID, input.name, temporary, len(input.body))
	if err != nil {
		abort(uploadDatabaseStatus(err))
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		abort(503)
		return
	}
	finalKey := fmt.Sprintf("%d-%s%s", id, random, strings.ToLower(path.Ext(input.name)))
	if _, err = tx.ExecContext(r.Context(), "UPDATE materials SET storage_key=? WHERE id=?", finalKey, id); err != nil {
		abort(uploadDatabaseStatus(err))
		return
	}
	if s.step("knowledge") != nil {
		abort(503)
		return
	}
	entry, err := tx.ExecContext(r.Context(), "INSERT INTO knowledge_entries (material_id,class_id,extracted_text) VALUES (?,?,?)", id, user.ClassID, input.text)
	if err != nil {
		abort(uploadDatabaseStatus(err))
		return
	}
	entryID, err := entry.LastInsertId()
	if err != nil {
		abort(503)
		return
	}
	// 切片与材料、知识库记录同事务提交：任一失败都不会留下半成品。
	inserts := make([]rag.ChunkInsert, len(chunks))
	for index, chunk := range chunks {
		inserts[index] = rag.ChunkInsert{
			Index: chunk.Index, Start: chunk.Start, End: chunk.End, Text: chunk.Text,
			Strategy: splitOptions.EffectiveMode(), Status: chunkStatus,
		}
	}
	chunkRows, err := rag.InsertChunks(r.Context(), tx, uint64(id), user.ClassID, uint64(entryID), inserts)
	if err != nil {
		abort(uploadDatabaseStatus(err))
		return
	}
	if s.step("move") != nil {
		abort(500)
		return
	}
	// These paths are generated by the server within dedicated trusted directories.
	if err = os.Rename(filepath.Join(s.staging(), temporary), filepath.Join(s.directory, finalKey)); err != nil {
		abort(500)
		return
	}
	final = finalKey
	if s.step("before_commit") != nil {
		abort(503)
		return
	}
	if s.commit != nil {
		err = s.commit(tx)
	} else {
		err = tx.Commit()
	}
	if err != nil {
		s.ready.Store(false)
		log.Print("upload commit outcome unknown; preserve storage and restart for reconciliation")
		securityError(w, 503)
		return
	}
	// 嵌入发生在提交之后：网关失败只把切片标成 failed，材料与知识库记录保持有效，
	// 关键字检索不受影响，教师可以之后再发起一次显式重建。
	if s.rag != nil && len(chunkRows) > 0 {
		indexContext, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		if indexErr := s.rag.IndexChunks(indexContext, chunkRows); indexErr != nil {
			log.Print("chunk embedding failed; chunks stay pending or failed for a later rebuild")
		}
		cancel()
	}
	jsonReply(w, 201, map[string]int64{"material_id": id})
}

func uploadDatabaseStatus(err error) int {
	var databaseError *mysql.MySQLError
	if errors.As(err, &databaseError) {
		switch databaseError.Number {
		case 1062, 1048, 1264, 1366, 1406, 1451, 1452, 1644, 3819:
			return 500 // Deterministic integrity/data failures, not an outage.
		}
	}
	return 503
}

func (s *uploadStore) prepareDirectories() error {
	if !filepath.IsAbs(s.directory) || filepath.Clean(s.directory) == s.staging() {
		return errors.New("invalid storage configuration")
	}
	for _, dir := range []string{s.directory, s.staging()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe storage directory")
		}
	}
	return nil
}
