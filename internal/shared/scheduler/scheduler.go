// Package scheduler is the lightweight in-process replacement for Django's
// Celery-beat periodic jobs (config/conf/celery.py CELERY_BEAT_SCHEDULE):
// one goroutine per job, each driven by its own time.Ticker, started from
// the server process. No separate worker/queue — this backend runs a single
// replica today, so an in-process ticker is the "lazy correct" fix (see
// ponytail note below); a job's own work is still just a normal usecase call
// against Postgres/k8s, so it's safe to run concurrently with request
// handling.
//
// ponytail: if this backend ever runs with multiple replicas, every replica
// will run every job on its own ticker, multiplying the work (e.g. the
// release-reconcile tick calling k8s N times instead of once, or the
// expiry/cleanup queries running redundantly). Each job below is written to
// tolerate that (they're all idempotent — reconciling/deleting/expiring an
// already-settled row is a no-op), so double-running is wasteful but not
// unsafe. Move to a real leader-election or a dedicated worker-golang cron
// only if that waste actually becomes a problem.
package scheduler

import (
	"context"
	"time"

	"github.com/botspace/backend/internal/shared/logger"
)

// Job is one periodic task: Run is called every Interval until ctx is
// cancelled. Run should be idempotent (see the package doc) and should do
// its own error logging is NOT required — the runner logs the returned
// error with the job Name attached.
type Job struct {
	Name     string
	Interval time.Duration
	Run      func(ctx context.Context) error
}

// Start launches one goroutine per job and returns immediately; every
// goroutine exits when ctx is cancelled. Each job runs on its own ticker so
// a slow job never delays another's schedule.
func Start(ctx context.Context, log *logger.Logger, jobs []Job) {
	for _, j := range jobs {
		go run(ctx, log, j)
	}
}

func run(ctx context.Context, log *logger.Logger, j Job) {
	ticker := time.NewTicker(j.Interval)
	defer ticker.Stop()
	log.Info("scheduler: job started",
		logger.String("job", j.Name),
		logger.Duration("interval", j.Interval),
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick(ctx, log, j)
		}
	}
}

// tick isolates one job's run behind a recover so a panic in a job (e.g. a
// nil-pointer bug in a rarely-hit cleanup path) can never take the whole
// process down — the request-serving goroutines are unaffected either way,
// but a crash-looping pod for a scheduler bug alone would be a bad trade.
func tick(ctx context.Context, log *logger.Logger, j Job) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("scheduler: job panicked",
				logger.String("job", j.Name),
				logger.Any("panic", r),
			)
		}
	}()
	start := time.Now()
	err := j.Run(ctx)
	fields := []logger.Field{
		logger.String("job", j.Name),
		logger.Duration("elapsed", time.Since(start)),
	}
	if err != nil {
		log.Error("scheduler: job failed", append(fields, logger.Err(err))...)
		return
	}
	log.Info("scheduler: job tick", fields...)
}
