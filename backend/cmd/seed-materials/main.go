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
	directory, err := seed.LoadUploadDir(getenv)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changed, err := seed.ApplyMaterial(ctx, pool, directory)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintln(out, "material seed initialized")
	} else {
		fmt.Fprintln(out, "no change")
	}
	return nil
}

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: seed-materials (DB_* and UPLOAD_DIR supplied via environment)")
		os.Exit(2)
	}
	if err := run(os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
