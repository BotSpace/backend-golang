package config

import "testing"

func TestLoadWorkerUsesCanonicalAndLegacyEnv(t *testing.T) {
	t.Setenv("APP_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_DSN", "")
	t.Setenv("APP_DB_HOST", "postgres")
	t.Setenv("APP_DB_PORT", "5432")
	t.Setenv("APP_DB_USER", "botspace")
	t.Setenv("APP_DB_PASSWORD", "secret/pass")
	t.Setenv("APP_DB_NAME", "botspace_go")
	t.Setenv("APP_DB_SSL_MODE", "require")
	t.Setenv("APP_REDIS_URL", "redis://canonical:6379")
	t.Setenv("REDIS_URL", "redis://legacy:6379")
	t.Setenv("MONGO_URL", "mongodb://legacy:27017")
	t.Setenv("STORAGE_URL", "https://storage.example")
	t.Setenv("STORAGE_ID", "storage-id")
	t.Setenv("STORAGE_KEY", "storage-key")
	t.Setenv("STORAGE_REGION", "eu")
	t.Setenv("STORAGE_BUCKET_MEDIA", "media")

	cfg, err := LoadWorker()
	if err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
	if cfg.RedisURL != "redis://canonical:6379" {
		t.Fatalf("RedisURL = %q, want canonical APP_REDIS_URL", cfg.RedisURL)
	}
	if cfg.MongoURL != "mongodb://legacy:27017" {
		t.Fatalf("MongoURL = %q, want legacy MONGO_URL fallback", cfg.MongoURL)
	}
	if cfg.StorageURL != "https://storage.example" || cfg.StorageBucketMedia != "media" {
		t.Fatalf("storage env not loaded: %+v", cfg)
	}
	wantDB := "postgres://botspace:secret%2Fpass@postgres:5432/botspace_go?sslmode=require"
	if cfg.DatabaseURL != wantDB {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, wantDB)
	}
}

func TestWorkerDatabaseURL(t *testing.T) {
	got := workerDatabaseURL(WorkerConfig{
		AppDBHost: "82.25.190.52",
		AppDBPort: "5432",
		AppDBUser: "botspace",
		AppDBPass: "secret/pass",
		AppDBName: "botspace_go",
		AppDBSSL:  "require",
	})
	want := "postgres://botspace:secret%2Fpass@82.25.190.52:5432/botspace_go?sslmode=require"
	if got != want {
		t.Fatalf("workerDatabaseURL() = %q, want %q", got, want)
	}
}

func TestWorkerDatabaseURLMissingRequired(t *testing.T) {
	got := workerDatabaseURL(WorkerConfig{AppDBHost: "postgres", AppDBUser: "botspace"})
	if got != "" {
		t.Fatalf("workerDatabaseURL() = %q, want empty", got)
	}
}
