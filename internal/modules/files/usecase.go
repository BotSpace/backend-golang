// Package files — celery cleanup_expired_files: muddati o'tgan fayllarni
// S3 storage'dan va DB'dan o'chiradi.
package files

import (
	"context"

	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/storagex"
)

type Usecase struct {
	Repo    Repository
	Storage *storagex.Storage
	Log     *zap.Logger
}

func (u *Usecase) Run(ctx context.Context) {
	rows, err := u.Repo.ListExpiredFiles(ctx)
	if err != nil {
		u.Log.Error("cleanup_expired_files: list", zap.Error(err))
		return
	}
	deleted := 0
	for _, row := range rows {
		// Django obj.file.delete(save=False) tartibi: avval storage, keyin DB qatori.
		if err := u.Storage.Delete(ctx, row.FilePath); err != nil {
			u.Log.Error("cleanup_expired_files: storage", zap.String("id", database.UUIDString(row.ID)), zap.Error(err))
			continue
		}
		if err := u.Repo.DeleteFile(ctx, row.ID); err != nil {
			u.Log.Error("cleanup_expired_files: db", zap.String("id", database.UUIDString(row.ID)), zap.Error(err))
			continue
		}
		deleted++
	}
	u.Log.Info("cleanup_expired_files done", zap.Int("deleted", deleted))
}
