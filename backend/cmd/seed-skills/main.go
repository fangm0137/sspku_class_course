// Command seed-skills 幂等地写入 A 班预置的两份技能样板。
// 与账号、材料初始化一样，它不随服务启动执行，需要显式运行。
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/seed"
)

func run(getenv func(string) string, out io.Writer) error {
	cfg, err := config.LoadDatabase(getenv)
	if err != nil {
		return err
	}
	pool, err := db.Open(cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changed, err := seed.ApplySkills(ctx, pool, seed.SeededSkills())
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintln(out, "skill seed initialized")
	} else {
		fmt.Fprintln(out, "no change")
	}
	return nil
}

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: seed-skills (DB_* supplied via environment)")
		os.Exit(2)
	}
	if err := run(os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
