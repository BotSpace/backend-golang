// Package debugsync — celery sync_debug: 30 daqiqadan oshgan RUNNING debug
// botlarni k8s'dan o'chirib, DB'da "stopping" qiladi (template loyihalar bundan
// mustasno — ular debug-demo sifatida doim ishlaydi).
package debugsync

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/k8sx"
	"github.com/redis/go-redis/v9"
)

const debugLifetime = 30 * time.Minute

type Usecase struct {
	Repo  Repository
	K8s   *k8sx.Client
	Log   *zap.Logger
	Redis *redis.Client
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
		if err := u.stopRuntime(ctx, *row.ServiceID); err != nil {
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

func (u *Usecase) stopRuntime(ctx context.Context, serviceID string) error {
	const prefix = "user-runtime/"
	if !strings.HasPrefix(serviceID, prefix) {
		return u.K8s.RemoveDeployment(ctx, serviceID)
	}
	parts := strings.Split(strings.TrimPrefix(serviceID, prefix), "/")
	if len(parts) != 2 {
		return u.K8s.RemoveDeployment(ctx, serviceID)
	}
	ownerID, webhookID := parts[0], parts[1]
	if u.Redis != nil {
		if err := u.Redis.HDel(ctx, "engine:user:"+ownerID+":runtimes", webhookID).Err(); err != nil {
			return err
		}
		_ = u.Redis.HDel(ctx, "engine:user:"+ownerID+":status", webhookID).Err()
		_ = u.Redis.XAdd(ctx, &redis.XAddArgs{Stream: "engine:user:" + ownerID + ":commands", MaxLen: 1000, Approx: true, Values: map[string]any{"action": "remove", "webhook_id": webhookID, "ts": time.Now().UnixMilli()}}).Err()
	}
	return u.K8s.RemoveBotRoute(ctx, webhookID)
}
