package seed

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"campusclaw/backend/fixtures"
)

const MaterialKey = "seed-class-b-v1.txt"

func LoadUploadDir(getenv func(string) string) (string, error) {
	path := getenv("UPLOAD_DIR")
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", errors.New("invalid required configuration: UPLOAD_DIR must be absolute")
	}
	return filepath.Clean(path), nil
}

// ApplyMaterial is serial-only. It never repairs/overwrites inconsistent seed state.
func ApplyMaterial(ctx context.Context, pool *sql.DB, directory string) (bool, error) {
	failure := errors.New("material seed initialization failed; inspect database and storage before retrying")
	conflict := errors.New("material seed state conflicts with expected fixture")
	if !filepath.IsAbs(directory) {
		return false, errors.New("UPLOAD_DIR must be absolute")
	}
	content, err := fixtures.Files.ReadFile("class-b-seed.txt")
	if err != nil {
		return false, failure
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return false, failure
	}
	defer tx.Rollback()
	var count int
	var classID sql.NullInt64
	if tx.QueryRowContext(ctx, "SELECT COUNT(*),MIN(id) FROM classes WHERE name=?", "B").Scan(&count, &classID) != nil {
		return false, failure
	}
	if count != 1 {
		return false, errors.New("initialize a unique class B before material seeding")
	}
	var id, storedClass uint64
	var uploader sql.NullInt64
	var name, source string
	var size int64
	err = tx.QueryRowContext(ctx, "SELECT id,class_id,uploader_id,original_name,source,size FROM materials WHERE storage_key=?", MaterialKey).Scan(&id, &storedClass, &uploader, &name, &source, &size)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, failure
	}
	if exists {
		if storedClass != uint64(classID.Int64) || uploader.Valid || name != "class-b-seed.txt" || source != "seed" || size != int64(len(content)) {
			return false, conflict
		}
		var text string
		var knowledgeClass uint64
		if tx.QueryRowContext(ctx, "SELECT class_id,extracted_text FROM knowledge_entries WHERE material_id=?", id).Scan(&knowledgeClass, &text) != nil || knowledgeClass != storedClass || text != string(content) {
			return false, conflict
		}
	} else if err = os.MkdirAll(directory, 0700); err != nil {
		return false, failure
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false, failure
	}
	defer root.Close()
	if exists {
		info, err := root.Lstat(MaterialKey)
		if err != nil || !info.Mode().IsRegular() {
			return false, conflict
		}
		file, err := root.Open(MaterialKey)
		if err != nil {
			return false, failure
		}
		actual, readErr := io.ReadAll(io.LimitReader(file, int64(len(content)+1)))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(actual, content) {
			return false, conflict
		}
		if tx.Commit() != nil {
			return false, failure
		}
		return false, nil
	}
	// A preexisting unreferenced file is a conflict, not permission to overwrite it.
	file, err := root.OpenFile(MaterialKey, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false, conflict
	}
	n, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || n != len(content) {
		return false, failure
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO materials (class_id,uploader_id,original_name,storage_key,size,source) VALUES (?,NULL,?,?,?,?)", classID.Int64, "class-b-seed.txt", MaterialKey, len(content), "seed")
	if err != nil {
		return false, failure
	}
	materialID, err := result.LastInsertId()
	if err != nil {
		return false, failure
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_entries (material_id,class_id,extracted_text) VALUES (?,?,?)", materialID, classID.Int64, string(content)); err != nil {
		return false, failure
	}
	if tx.Commit() != nil {
		return false, failure
	}
	return true, nil
}
