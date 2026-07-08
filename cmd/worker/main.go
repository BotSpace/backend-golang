// worker — backend celery worker+beat o'rnini bosuvchi Go servis.
//
// Periodik (celery beat bilan bir xil jadval):
//
//	sync_debug               */5 daqiqa
//	cleanup_expired_files    */5 daqiqa
//	cleanup_failed_releases  */10 daqiqa
//	sync_bot                 har soat :00
//	snapshot_project_disk    har soat :00
//
// Periodik, net-new (Django'da hech qachon bo'lmagan — backend-golang'ning
// o'z in-process scheduler'idan (internal/server/scheduler.go) shu yerga
// ko'chirilgan, chunki periodik joblar API process'da emas, worker'da
// yashashi kerak):
//
//	expire_subscriptions             har soat :00
//	cleanup_expired_refresh_tokens   har soat :00
//	cleanup_expired_authbot_codes    har soat :00
//	orphan_sweep                     */15 daqiqa  (leaked engine Deployment'lar)
//
// On-demand (Redis Stream goworker:tasks, Django web XADD qiladi):
//
//	send_message           — broadcast
//	send_telegram_message  — CRM chat javobi
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/botspace/backend/internal/modules/authcleanup"
	"github.com/botspace/backend/internal/modules/botsync"
	"github.com/botspace/backend/internal/modules/broadcast"
	"github.com/botspace/backend/internal/modules/chatsend"
	"github.com/botspace/backend/internal/modules/collabpersist"
	"github.com/botspace/backend/internal/modules/debugsync"
	"github.com/botspace/backend/internal/modules/disksnap"
	"github.com/botspace/backend/internal/modules/files"
	"github.com/botspace/backend/internal/modules/orphansweep"
	"github.com/botspace/backend/internal/modules/releases"
	"github.com/botspace/backend/internal/modules/subscriptions"
	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/k8sx"
	"github.com/botspace/backend/internal/shared/logger"
	"github.com/botspace/backend/internal/shared/mongox"
	"github.com/botspace/backend/internal/shared/redisq"
	"github.com/botspace/backend/internal/shared/storagex"
)

func main() {
	log, err := logger.New("production", "info")
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()

	cfg, err := config.LoadWorker()
	if err != nil {
		log.Fatal("config", zap.Error(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewWorkerPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pool.Close()

	mongo, err := mongox.New(ctx, cfg.MongoURL)
	if err != nil {
		log.Fatal("mongo", zap.Error(err))
	}

	k8s, err := k8sx.New(cfg.K8sNamespace, log)
	if err != nil {
		log.Fatal("k8s", zap.Error(err))
	}

	storage, err := storagex.New(cfg.StorageURL, cfg.StorageID, cfg.StorageKey, cfg.StorageRegion, cfg.StorageBucketMedia)
	if err != nil {
		log.Fatal("storage", zap.Error(err))
	}

	// --- Usecase'lar ---
	debugSync := &debugsync.Usecase{Repo: debugsync.NewPostgresRepository(pool), K8s: k8s, Log: log}
	fileCleanup := &files.Usecase{Repo: files.NewPostgresRepository(pool), Storage: storage, Log: log}
	releaseCleanup := &releases.Usecase{Repo: releases.NewPostgresRepository(pool), K8s: k8s, Log: log}
	// Orphan sweep: net-new periodik job (Django'da yo'q). ORPHAN_SWEEP_DRY_RUN=true
	// bo'lsa faqat log'laydi, o'chirmaydi (birinchi ishga tushirishda tekshirish uchun).
	orphanSweep := &orphansweep.Usecase{
		Repo:   orphansweep.NewPostgresRepository(pool),
		K8s:    k8s,
		Log:    log,
		DryRun: os.Getenv("ORPHAN_SWEEP_DRY_RUN") == "true",
	}
	rdbClient, err := redisq.NewURLClient(cfg.RedisURL)
	if err != nil {
		log.Fatal("redis client", zap.Error(err))
	}
	botSync := &botsync.Usecase{Repo: botsync.NewPostgresRepository(pool), Redis: rdbClient, Log: log}
	diskSnap := &disksnap.Usecase{Repo: disksnap.NewPostgresRepository(pool), Mongo: mongo, Log: log}
	bcast := &broadcast.Usecase{Repo: broadcast.NewPostgresRepository(pool), Mongo: mongo, Storage: storage, Log: log}
	chatSend := &chatsend.Usecase{Repo: chatsend.NewPostgresRepository(pool), Storage: storage, Log: log}
	subsExpiry := &subscriptions.Usecase{Repo: subscriptions.NewPostgresRepository(pool), Log: log}
	authClean := &authcleanup.Usecase{Repo: authcleanup.NewPostgresRepository(pool), Log: log}
	collabPersist := &collabpersist.Usecase{Repo: collabpersist.NewPostgresRepository(pool), Redis: rdbClient, Log: log}

	// --- Cron (celery beat jadvali) ---
	c := cron.New()
	mustAdd := func(spec string, name string, fn func(context.Context)) {
		_, err := c.AddFunc(spec, func() { fn(ctx) })
		if err != nil {
			log.Fatal("cron add", zap.String("job", name), zap.Error(err))
		}
	}
	mustAddIfTables := func(spec string, name string, fn func(context.Context), tables ...string) {
		for _, table := range tables {
			ok, err := tableExists(ctx, pool, table)
			if err != nil {
				log.Warn("cron schema check failed; registering job anyway",
					zap.String("job", name),
					zap.String("table", table),
					zap.Error(err))
				break
			}
			if !ok {
				log.Warn("cron job disabled: required table does not exist",
					zap.String("job", name),
					zap.String("table", table))
				return
			}
		}
		mustAdd(spec, name, fn)
	}
	mustAdd("*/5 * * * *", "sync_debug", debugSync.Run)
	mustAdd("*/5 * * * *", "cleanup_expired_files", fileCleanup.Run)
	mustAdd("*/10 * * * *", "cleanup_failed_releases", releaseCleanup.Run)
	mustAdd("*/15 * * * *", "orphan_sweep", orphanSweep.Run)
	mustAdd("0 * * * *", "sync_bot", botSync.Run)
	mustAdd("0 * * * *", "snapshot_project_disk", diskSnap.Run)
	mustAddIfTables("0 * * * *", "expire_subscriptions", subsExpiry.Run, "user_subscription")
	mustAddIfTables("0 * * * *", "cleanup_expired_refresh_tokens", authClean.RunRefreshTokens, "refresh_tokens")
	mustAddIfTables("0 * * * *", "cleanup_expired_authbot_codes", authClean.RunAuthbotCodes, "authbot_codes")
	c.Start()
	defer c.Stop()

	// --- Redis Stream consumer (on-demand tasklar) ---
	consumer, err := redisq.NewURLConsumer(cfg.RedisURL, cfg.TaskStream, cfg.TaskConsumerGroup, "worker-1", log)
	if err != nil {
		log.Fatal("redis", zap.Error(err))
	}
	consumer.Register("send_message", bcast.Handle)
	consumer.Register("send_telegram_message", chatSend.Handle)
	// RELEASE_BOT_TOKEN o'zgarganda backend signal shu taskni enqueue qiladi.
	consumer.Register("sync_bot", func(ctx context.Context, _ json.RawMessage) error {
		botSync.Run(ctx)
		return nil
	})
	go collabPersist.Run(ctx)

	log.Info("worker started",
		zap.String("stream", cfg.TaskStream),
		zap.String("namespace", cfg.K8sNamespace))

	if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal("consumer", zap.Error(err))
	}
	log.Info("worker stopped")
}

func tableExists(ctx context.Context, pool *pgxpool.Pool, table string) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&ok)
	return ok, err
}
