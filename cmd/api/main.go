package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/botspace/backend/docs"
	"github.com/botspace/backend/internal/server"
	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/logger"
	"go.uber.org/zap"
)

// @title           jst-go API
// @version         1.0
// @description     Go clean architecture template — gin + pgx + sqlc + JWT auth.
// @termsOfService  http://swagger.io/terms/
//
// @contact.name   UzStack
// @contact.url    https://github.com/botspace/backend
//
// @license.name  MIT
//
// @host      localhost:8080
// @BasePath  /api/v1
//
// @securityDefinitions.apikey BearerAuth
// @in                         header
// @name                       Authorization
// @description                Type "Bearer <access_token>" in the Value field.

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic("config load: " + err.Error())
	}

	log, err := logger.New(cfg.Env, cfg.Log.Level)
	if err != nil {
		panic("logger init: " + err.Error())
	}
	undoGlobalLog := zap.ReplaceGlobals(log)
	defer undoGlobalLog()
	defer func() { _ = log.Sync() }()

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := database.NewPool(rootCtx, cfg.DB)
	if err != nil {
		log.Fatal("db connect failed", logger.Err(err))
	}
	defer pool.Close()

	if cfg.DB.AutoMigrate {
		if err := database.MigrateUp(cfg.DB.MigrationsURL(), cfg.DB.MigrationsPath); err != nil {
			log.Fatal("migration failed", logger.Err(err))
		}
	} else {
		log.Info("auto-migrate disabled; run migrations via CLI (make migrate-up)")
	}

	srv, err := server.New(rootCtx, cfg, log, pool)
	if err != nil {
		log.Fatal("server init failed", logger.Err(err))
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTP.Addr(),
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server starting", logger.String("addr", httpServer.Addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		log.Error("http server error", logger.Err(err))
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", logger.Err(err))
		os.Exit(1)
	}

	log.Info("server stopped cleanly")
}
