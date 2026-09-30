package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

func main() {
	if err := run(os.Getenv, func(server *http.Server) error { return server.ListenAndServe() }); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(getenv func(string) string, serve func(*http.Server) error) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	pool, err := db.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: httpapi.NewApplication(pool, cfg.TokenSecret, cfg.UploadDir),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	if err := serve(server); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("HTTP server failed to start or serve")
	}
	return nil
}
