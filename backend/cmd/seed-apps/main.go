// Command seed-apps 幂等地写入 A 班的内置 AI 应用（数学助教、语文助教、通用答疑）。
// 它在账号、技能与材料初始化之后运行；与其它种子一样，不随服务启动执行。
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
	changed, err := seed.ApplyApps(ctx, pool, seed.SeededApps())
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintln(out, "app seed initialized")
	} else {
		fmt.Fprintln(out, "no change")
	}
	return nil
}

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: seed-apps (DB_* supplied via environment)")
		os.Exit(2)
	}
	if err := run(os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
