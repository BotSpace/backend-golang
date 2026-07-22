package runtimemigration

import (
	"context"

	"go.uber.org/zap"
)

func Run(ctx context.Context, migrator Migrator, repo Repository, log *zap.Logger) {
	count, err := migrator.MigrateLegacyRuntimes(ctx, repo.OwnerForService, repo.ReplaceServiceID)
	if err != nil {
		log.Error("user runtime migration failed", zap.Error(err))
		return
	}
	log.Info("user runtime migration complete", zap.Int("migrated", count))
}
