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
	passwords, err := seed.LoadPasswords(getenv)
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
	changed, err := seed.Apply(ctx, pool, passwords)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintln(out, "accounts initialized")
	} else {
		fmt.Fprintln(out, "no change")
	}
	return nil
}

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: seed (DB_* and SEED_*_PASSWORD supplied via environment)")
		os.Exit(2)
	}
	if err := run(os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
