// Command reindex 对已入库材料执行一次显式重建索引。
// 它是第 4 课迁移计划里的 backfill 步骤：第 3 课的历史材料上传时还没有切片，
// 需要用这次变更的切分策略补建切片与向量。命令失败不自动重试，需要人工重跑。
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/rag"
)

func main() {
	if err := run(os.Getenv, os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(getenv func(string) string, args []string) error {
	ids, err := parseIDs(args)
	if err != nil {
		return err
	}
	cfg, err := config.LoadDatabase(getenv)
	if err != nil {
		return err
	}
	pool, err := db.Open(cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	service := rag.New(pool, rag.FromEnv(getenv))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if !service.VectorEnabled() {
		fmt.Println("warning: EMBEDDING_* is not configured; only keyword chunks will be written")
	} else if err := service.EnsureCollection(ctx); err != nil {
		return errors.New("qdrant collection is not ready")
	}
	targets, err := targets(ctx, pool, ids)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Println("no material to rebuild")
		return nil
	}
	failures := 0
	for _, target := range targets {
		count, err := service.RebuildIndex(ctx, target.id, target.classID, rag.SplitOptions{Mode: rag.ModeAuto})
		if err != nil {
			failures++
			fmt.Printf("material %d (class %d): rebuild failed\n", target.id, target.classID)
			continue
		}
		fmt.Printf("material %d (class %d): rebuilt %d chunk(s)\n", target.id, target.classID, count)
	}
	if failures > 0 {
		return fmt.Errorf("%d material(s) failed to rebuild", failures)
	}
	return nil
}

type target struct {
	id      uint64
	classID uint64
}

// parseIDs 只接受正整数；不传参数表示全部材料。
func parseIDs(args []string) ([]uint64, error) {
	ids := make([]uint64, 0, len(args))
	for _, raw := range args {
		for _, piece := range strings.Split(raw, ",") {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			value, err := strconv.ParseUint(piece, 10, 64)
			if err != nil || value == 0 {
				return nil, errors.New("material ids must be positive integers")
			}
			ids = append(ids, value)
		}
	}
	return ids, nil
}

func targets(ctx context.Context, pool *sql.DB, ids []uint64) ([]target, error) {
	query := "SELECT id, class_id FROM materials ORDER BY id"
	args := make([]any, 0, len(ids))
	if len(ids) > 0 {
		placeholders := make([]string, len(ids))
		for index, id := range ids {
			placeholders[index] = "?"
			args = append(args, id)
		}
		query = "SELECT id, class_id FROM materials WHERE id IN (" + strings.Join(placeholders, ",") + ") ORDER BY id"
	}
	rows, err := pool.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make([]target, 0)
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.id, &item.classID); err != nil {
			return nil, err
		}
		found = append(found, item)
	}
	return found, rows.Err()
}
