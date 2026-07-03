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
