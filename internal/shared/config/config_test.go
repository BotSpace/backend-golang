package config

import "testing"

func TestDSN_EscapesSpecialChars(t *testing.T) {
	db := DBConfig{
		Host: "localhost", Port: 5432, User: "post@gres",
		Password: "p@ss:w/rd?#", Name: "app", SSLMode: "disable",
	}
	// Special chars in credentials must be percent-encoded, not break the URL.
	got := db.DSN()
	want := "postgres://post%40gres:p%40ss%3Aw%2Frd%3F%23@localhost:5432/app?sslmode=disable"
	if got != want {
		t.Fatalf("DSN()\n got: %s\nwant: %s", got, want)
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	// A real APP_* env var (also how a loaded .env value arrives) must override
	// config.yaml. Without this, .env values were silently ignored.
	t.Setenv("APP_DB_PASSWORD", "from_env")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DB.Password != "from_env" {
		t.Errorf("DB.Password = %q, want from_env (env should beat config.yaml)", cfg.DB.Password)
	}
}

func TestLoad_MapsSharedEnvToEngineConfig(t *testing.T) {
	t.Setenv("APP_INTERNAL_TOKEN", "shared-token")
	t.Setenv("APP_MONGO_URL", "mongodb://mongo:27017")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.InternalToken != "shared-token" {
		t.Fatalf("Engine.InternalToken = %q, want shared-token", cfg.Engine.InternalToken)
	}
	if cfg.Engine.MongoAddr != "mongodb://mongo:27017" {
		t.Fatalf("Engine.MongoAddr = %q, want mongodb://mongo:27017", cfg.Engine.MongoAddr)
	}
}

func TestLoad_EngineSpecificEnvWinsOverSharedFallback(t *testing.T) {
	t.Setenv("APP_INTERNAL_TOKEN", "shared-token")
	t.Setenv("APP_ENGINE_INTERNAL_TOKEN", "engine-token")
	t.Setenv("APP_MONGO_URL", "mongodb://shared:27017")
	t.Setenv("APP_ENGINE_MONGO_ADDR", "mongodb://engine:27017")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.InternalToken != "engine-token" {
		t.Fatalf("Engine.InternalToken = %q, want engine-token", cfg.Engine.InternalToken)
	}
	if cfg.Engine.MongoAddr != "mongodb://engine:27017" {
		t.Fatalf("Engine.MongoAddr = %q, want mongodb://engine:27017", cfg.Engine.MongoAddr)
	}
}

func TestLoad_MapsPluginAPIKeyIntoEngineConfig(t *testing.T) {
	t.Setenv("APP_ENGINE_PLUGIN_API_KEY", "plugin-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engine.PluginAPIKey != "plugin-secret" {
		t.Fatalf("Engine.PluginAPIKey = %q, want plugin-secret", cfg.Engine.PluginAPIKey)
	}
}

func TestValidate_RequiresKeyPaths(t *testing.T) {
	// missing key paths fail
	missing := &Config{DB: DBConfig{Host: "db"}}
	if err := missing.validate(); err == nil {
		t.Error("expected error when jwt key paths are empty")
	}
	// both set passes
	ok := &Config{
		DB:  DBConfig{Host: "db"},
		JWT: JWTConfig{PrivateKeyPath: "keys/p.pem", PublicKeyPath: "keys/pub.pem"},
	}
	if err := ok.validate(); err != nil {
		t.Errorf("expected valid config, got %v", err)
	}
}

func validProductionConfig(t *testing.T) *Config {
	t.Helper()
	t.Setenv("APP_CRYPTO_KEY", "a-real-32-byte-production-secret")
	t.Setenv("APP_INTERNAL_TOKEN", "a-real-internal-production-secret")
	return &Config{
		Env: "production",
		DB:  DBConfig{Host: "db"},
		JWT: JWTConfig{PrivateKeyPath: "keys/p.pem", PublicKeyPath: "keys/pub.pem"},
		HTTP: HTTPConfig{
			CORSOrigins:  []string{"https://botspace.uz"},
			RateLimitRPS: 20,
		},
		OTP: OTPConfig{Prod: true},
	}
}

func TestValidate_ProductionRejectsUnsafeDefaults(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"wildcard cors", func(c *Config) { c.HTTP.CORSOrigins = []string{"*"} }},
		{"disabled rate limit", func(c *Config) { c.HTTP.RateLimitRPS = 0 }},
		{"fixed otp", func(c *Config) { c.OTP.Prod = false }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validProductionConfig(t)
			tc.mutate(cfg)
			if err := cfg.validate(); err == nil {
				t.Fatal("expected unsafe production config to fail")
			}
		})
	}
}

func TestValidate_ProductionRequiresRealSecrets(t *testing.T) {
	cfg := validProductionConfig(t)
	t.Setenv("APP_CRYPTO_KEY", "change-me-in-production")
	if err := cfg.validate(); err == nil {
		t.Fatal("expected placeholder crypto key to fail")
	}

	cfg = validProductionConfig(t)
	t.Setenv("APP_INTERNAL_TOKEN", "change-me-internal")
	if err := cfg.validate(); err == nil {
		t.Fatal("expected placeholder internal token to fail")
	}
}
