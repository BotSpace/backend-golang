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
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func main() {
	idsFlag := flag.String("ids", "", "comma-separated numeric legacy project ids")
	sharedFlag := flag.Bool("shared", false, "migrate UUID and per-owner runtimes into the shared pool")
	limitFlag := flag.Int("limit", 0, "maximum shared runtimes to attempt; zero means all")
	legacyProjectsFlag := flag.String("legacy-projects", "", "comma-separated legacy_id=project_uuid mappings for the external Django database")
	flag.Parse()
	if *limitFlag < 0 {
		panic("--limit cannot be negative")
	}
	var ids []int32
	var err error
	legacyProjects, err := parseLegacyProjects(*legacyProjectsFlag)
	if err != nil {
		panic(err)
	}
	if !*sharedFlag {
		ids, err = parseIDs(*idsFlag)
		if err != nil {
			panic(err)
		}
	} else if strings.TrimSpace(*idsFlag) != "" {
		panic("--ids and --shared cannot be used together")
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
	if *sharedFlag {
		migrationCtx, migrationCancel := context.WithTimeout(ctx, 30*time.Minute)
		count, migrationErr := orch.MigrateLegacyRuntimesLimited(migrationCtx, *limitFlag, repo.OwnerForService, repo.ReplaceServiceID)
		migrationCancel()
		if migrationErr != nil {
			log.Fatal("shared runtime migration failed", zap.Int("migrated", count), zap.Error(migrationErr))
		}
		log.Info("shared runtime migration complete", zap.Int("migrated", count), zap.Int("limit", *limitFlag))
		return
	}

	failed := false
	for _, legacyID := range ids {
		itemCtx, itemCancel := context.WithTimeout(ctx, 3*time.Minute)
		resolve := repo.ResolveLegacyProject
		replace := repo.ReplaceServiceID
		if projectID, external := legacyProjects[legacyID]; external {
			resolve = func(context.Context, int32) (uuid.UUID, uuid.UUID, error) {
				// Shared-pool placement is account-independent. The project UUID is
				// used as the temporary owner identity because the external Django
				// account still has a numeric primary key.
				return projectID, projectID, nil
			}
			replace = func(context.Context, uuid.UUID, string, string, bool) (bool, error) {
				// The operator updates RelaseModel through Django ORM after the
				// pool child is ready and this command reports its ServiceID.
				return true, nil
			}
		}
		result, migrateErr := orch.MigrateLegacyNumericRuntime(itemCtx, legacyID, resolve, replace)
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

func parseLegacyProjects(raw string) (map[int32]uuid.UUID, error) {
	out := map[int32]uuid.UUID{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid legacy project mapping %q", item)
		}
		legacyID, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 32)
		if err != nil || legacyID <= 0 {
			return nil, fmt.Errorf("invalid legacy id in mapping %q", item)
		}
		projectID, err := uuid.Parse(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid project uuid in mapping %q: %w", item, err)
		}
		out[int32(legacyID)] = projectID
	}
	return out, nil
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
