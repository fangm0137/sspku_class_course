package main

import (
	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/schema"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "up" {
		fmt.Fprintln(os.Stderr, "usage: migrate up (connection supplied via DB_* environment)")
		os.Exit(2)
	}
	cfg, err := config.LoadDatabase(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	changed, err := schema.Up(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if changed {
		fmt.Println("migrations applied")
	} else {
		fmt.Println("no change")
	}
}
