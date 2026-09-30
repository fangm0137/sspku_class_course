package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/rag"
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
	retrieval := rag.New(pool, rag.FromEnv(getenv))
	// 集合初始化失败不阻止启动：keyword 仍可用，vector/hybrid 会如实返回 503。
	// 未配置嵌入网关时这一步是空操作，启动不依赖任何外部服务。
	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 15*time.Second)
	if err := retrieval.EnsureCollection(ensureCtx); err != nil {
		log.Print("qdrant collection is not ready; vector retrieval reports dependency failure until it is")
	}
	cancelEnsure()
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: httpapi.NewApplication(pool, cfg.TokenSecret, cfg.UploadDir, retrieval),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	if err := serve(server); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("HTTP server failed to start or serve")
	}
	return nil
}
