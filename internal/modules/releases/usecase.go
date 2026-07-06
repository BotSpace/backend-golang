// Package releases — celery cleanup_failed_releases: CrashLoop holatidagi
// release VA debug engine deployment'larni k8s'dan o'chiradi (cluster-driven —
// faqat pod'ning o'z holatiga qaraydi, biror DB'da release/debug yozuvi bor-
// yo'qligidan qat'i nazar), so'ngra best-effort DB'da status=failed qiladi.
//
// Muhim: bu DB (botspace, Django) faqat ESKI backend orqali deploy qilingan
// release/debug'larni biladi. Yangi Go backend (botspace_go) o'z alohida
// DB'sida release/debug'larni kuzatadi — bu yerdan hech qachon ko'rinmaydi.
// Shu sabab k8s tozalash DB'dan MUTLAQO mustaqil: pod label'i orqali
// aniqlanadi, o'chiriladi, keyin DB yangilanishi urinib ko'riladi va har doim
// best-effort (topilmasa/xato bo'lsa ham k8s tozalash allaqachon amalga
// oshgan bo'ladi — process davom etadi).
package releases

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/k8sx"
)

type Usecase struct {
	Repo Repository
	K8s  *k8sx.Client
	Log  *zap.Logger
}

func (u *Usecase) Run(ctx context.Context) {
	removed, err := u.K8s.CleanupCrashLoopingEngines(ctx, time.Now())
	if err != nil {
		u.Log.Error("cleanup_failed_releases: list pods", zap.Error(err))
		return
	}

	for _, eng := range removed {
		u.Log.Warn("crashloop cleaned", zap.String("engine", eng.Name))

		// DB status yangilash — best-effort, hech qachon k8s tozalashni
		// to'xtatmaydi. 0 rows affected (bu DB'da yozuv yo'q — masalan
		// botspace_go'da deploy qilingan bot) xato emas, shunchaki no-op.
		u.markFailed(ctx, eng.Name, eng.Kind)
	}
	if len(removed) > 0 {
		u.Log.Info("cleanup_failed_releases done", zap.Int("cleaned", len(removed)))
	}
}

func (u *Usecase) markFailed(ctx context.Context, name string, kind k8sx.EngineKind) {
	var rows int64
	var err error
	switch kind {
	case k8sx.EngineKindRelease:
		rows, err = u.Repo.MarkReleaseFailedByService(ctx, name)
	case k8sx.EngineKindDebug:
		rows, err = u.Repo.MarkDebugFailedByService(ctx, name)
	default:
		return
	}
	if err != nil {
		u.Log.Warn("cleanup_failed_releases: db update (non-fatal, k8s already cleaned)",
			zap.String("engine", name), zap.Error(err))
		return
	}
	u.Log.Info("db status marked failed", zap.String("engine", name), zap.Int64("rows", rows))
}
