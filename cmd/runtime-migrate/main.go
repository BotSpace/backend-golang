// runtime-migrate is an operator-only, stop-first migration command. It runs
// inside the backend pod so it reuses the production DB/Kubernetes/Redis
// credentials, and only touches the explicit comma-separated legacy ids.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/botspace/backend/internal/modules/runtimemigration"
	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/logger"
	"go.uber.org/zap"
)

func main() {
	idsFlag := flag.String("ids", "", "comma-separated numeric legacy project ids")
	flag.Parse()
	ids, err := parseIDs(*idsFlag)
	if err != nil {
		panic(err)
	}

	cfg, err := config.Load()
	if err != nil {
		panic("config load: " + err.Error())
	}
	log, err := logger.New(cfg.Env, cfg.Log.Level)
	if err != nil {
		panic(err)
	}
	defer log.Sync()
	undo := zap.ReplaceGlobals(log)
	defer undo()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	pool, err := database.NewPool(ctx, cfg.DB)
	if err != nil {
		log.Fatal("db connect failed", zap.Error(err))
	}
	defer pool.Close()
	orch, err := engine.NewKubernetesOrchestrator(engine.KubernetesConfig{Namespace: cfg.Engine.Namespace, ImageRepository: cfg.Engine.ImageRepository, ImageTag: cfg.Engine.ImageTag, RegistrySecretName: cfg.Engine.RegistrySecretName, APIURL: cfg.Engine.APIURL, InternalToken: cfg.Engine.InternalToken, TestBotToken: cfg.Engine.TestBotToken, RedisAddr: cfg.Engine.RedisAddr(), RedisPassword: cfg.Engine.RedisPassword, MongoAddr: cfg.Engine.MongoAddr, MongoDBName: cfg.Engine.MongoDBName})
	if err != nil {
		log.Fatal("orchestrator init failed", zap.Error(err))
	}
	repo := runtimemigration.NewPostgresRepository(pool)

	failed := false
	for _, legacyID := range ids {
		itemCtx, itemCancel := context.WithTimeout(ctx, 3*time.Minute)
		result, migrateErr := orch.MigrateLegacyNumericRuntime(itemCtx, legacyID, repo.ResolveLegacyProject, repo.ReplaceServiceID)
		itemCancel()
		if migrateErr != nil {
			failed = true
			log.Error("legacy runtime migration failed", zap.Int32("legacy_id", legacyID), zap.Error(migrateErr))
			continue
		}
		log.Info("legacy runtime migrated", zap.Int32("legacy_id", legacyID), zap.String("project_id", result.ProjectID.String()), zap.String("owner_id", result.OwnerID.String()), zap.String("old_deployment", result.OldDeployment), zap.String("webhook_id", result.WebhookID.String()))
	}
	if failed {
		os.Exit(1)
	}
}

func parseIDs(raw string) ([]int32, error) {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	if len(parts) == 1 && strings.TrimSpace(parts[0]) == "" {
		return nil, fmt.Errorf("--ids is required")
	}
	ids := make([]int32, 0, len(parts))
	seen := map[int32]bool{}
	for _, part := range parts {
		value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 32)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("invalid legacy id %q", part)
		}
		id := int32(value)
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
