// Package orphansweep — periodik orphan bot-engine tozalash (Django'da yo'q edi;
// backend-golang net-new). StopActiveReleasesExcept reaping'ini to'ldiradi.
//
// Muammo (Faza 2.5): eski per-bot Deployment'lar sizib qoladi. Reaping faqat
// service_id'si DB'da hali "tirik" (terminal emas) release/debug bilan bog'liq
// Deployment'larni to'xtatadi; agar owning yozuv yo'qolgan/stopped bo'lsa-yu
// Deployment qolib ketgan bo'lsa (eski filter bug'i, qo'lda o'zgarish, jarayon
// halok bo'lishi), u orphan bo'lib qoladi. Bu sweep har safar:
//
//  1. DB'dan hali tirik release/debug service_id'larini oladi (live to'plami);
//  2. app=botmother-engine label'li release-*/debug-* Deployment'larni ko'radi;
//  3. live to'plamida YO'Q bo'lganlarini (Deployment+Secret+ConfigMap+Service)
//     to'liq o'chiradi.
//
// XAVFSIZLIK: faqat engine label'li release-*/debug-* resurslar; module-* shared
// pod'lar tegilmaydi; endigina yaratilgan (grace davridagi) Deployment'lar
// o'tkazib yuboriladi; DryRun=true bo'lsa hech narsa o'chirilmaydi.
package orphansweep

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
	// DryRun — true bo'lsa orphan'lar faqat log'lanadi, o'chirilmaydi
	// (ORPHAN_SWEEP_DRY_RUN=true env orqali yoqiladi).
	DryRun bool
}

func (u *Usecase) Run(ctx context.Context) {
	ids, err := u.Repo.ListLiveEngineServiceIDs(ctx)
	if err != nil {
		u.Log.Error("orphan_sweep: list live service ids", zap.Error(err))
		return
	}
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		live[id] = true
	}

	removed, err := u.K8s.SweepOrphanEngines(ctx, live, u.DryRun, time.Now())
	if err != nil {
		u.Log.Error("orphan_sweep: list deployments", zap.Error(err))
		return
	}
	if len(removed) > 0 {
		u.Log.Info("orphan_sweep done",
			zap.Int("orphans", len(removed)),
			zap.Bool("dry_run", u.DryRun))
	}
}
