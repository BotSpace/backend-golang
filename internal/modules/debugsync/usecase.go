// Package debugsync — celery sync_debug: 30 daqiqadan oshgan RUNNING debug
// botlarni k8s'dan o'chirib, DB'da "stopping" qiladi (template loyihalar bundan
// mustasno — ular debug-demo sifatida doim ishlaydi).
package debugsync

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/k8sx"
)

const debugLifetime = 30 * time.Minute

type Usecase struct {
	Repo Repository
	K8s  *k8sx.Client
	Log  *zap.Logger
}

func (u *Usecase) Run(ctx context.Context) {
	cutoff := time.Now().Add(-debugLifetime)
	rows, err := u.Repo.ListExpiredRunningDebugs(ctx, cutoff)
	if err != nil {
		u.Log.Error("sync_debug: list", zap.Error(err))
		return
	}
	for _, row := range rows {
		if row.ServiceID == nil || *row.ServiceID == "" {
			continue
		}
		if err := u.K8s.RemoveDeployment(ctx, *row.ServiceID); err != nil {
			u.Log.Error("sync_debug: remove", zap.String("service", *row.ServiceID), zap.Error(err))
			continue
		}
		if err := u.Repo.MarkDebugStopping(ctx, row.ID); err != nil {
			u.Log.Error("sync_debug: mark stopping", zap.String("id", database.UUIDString(row.ID)), zap.Error(err))
		}
	}
	if len(rows) > 0 {
		u.Log.Info("sync_debug done", zap.Int("stopped", len(rows)))
	}
}
