package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Recover performs one pass before business readiness. No automatic retries.
func (s *uploadStore) Recover(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready.Store(false)
	var count int
	// Readiness probe: every business table that a request depends on must be
	// queryable. Authentication is stateless now, so no session table is joined.
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM materials m LEFT JOIN knowledge_entries k ON k.material_id=m.id LEFT JOIN users u ON u.id=m.uploader_id LEFT JOIN classes c ON c.id=m.class_id WHERE 1=0").Scan(&count); err != nil {
		return err
	}
	if err := s.prepareDirectories(); err != nil {
		return err
	}
	for _, dir := range []string{s.staging(), s.directory} {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		err = s.cleanRoot(ctx, root, dir == s.staging())
		root.Close()
		if err != nil {
			return err
		}
	}
	s.ready.Store(true)
	return nil
}
func (s *uploadStore) cleanRoot(ctx context.Context, root *os.Root, staging bool) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := file.ReadDir(-1)
	file.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return errors.New("unexpected storage entry")
		}
		if !staging {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			var id uint64
			err = tx.QueryRowContext(ctx, "SELECT id FROM materials WHERE storage_key=? FOR UPDATE", entry.Name()).Scan(&id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				tx.Rollback()
				return err
			}
			keep := err == nil
			if keep && entry.Name() != "seed-class-b-v1.txt" {
				prefix, _, ok := strings.Cut(entry.Name(), "-")
				expected, e := strconv.ParseUint(prefix, 10, 64)
				if !ok || e != nil || expected != id {
					tx.Rollback()
					return errors.New("material identity mismatch")
				}
			}
			// Locking read waits for unresolved inserts before deciding a file is orphaned.
			if !keep {
				if err = s.removeRecovered(root, entry.Name()); err != nil {
					tx.Rollback()
					return err
				}
			}
			if err = tx.Commit(); err != nil {
				return err
			}
		} else {
			if err = s.removeRecovered(root, entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *uploadStore) removeRecovered(root *os.Root, name string) error {
	if err := s.step("recovery_cleanup"); err != nil {
		return err
	}
	return root.Remove(name)
}

// NewApplication starts health immediately and gates business on recovery.
func NewApplication(db *sql.DB, secret, directory string) http.Handler {
	store := newUploadStore(db, directory)
	store.ready.Store(false)
	handler := newHandler(db, secret, store)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if store.Recover(ctx) != nil {
			log.Print("business recovery failed; repair dependencies and restart")
		}
	}()
	return handler
}
