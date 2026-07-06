package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	Env     string           `mapstructure:"env"`
	HTTP    HTTPConfig       `mapstructure:"http"`
	DB      DBConfig         `mapstructure:"db"`
	JWT     JWTConfig        `mapstructure:"jwt"`
	OTP     OTPConfig        `mapstructure:"otp"`
	Log     LogConfig        `mapstructure:"log"`
	Engine  EngineConfig     `mapstructure:"engine"`
	Google  GoogleConfig     `mapstructure:"google"`
	Storage StorageConfig    `mapstructure:"storage"`
	Monitor MonitoringConfig `mapstructure:"monitoring"`
}

// GoogleConfig configures Google sign-in (POST /api/v1/auth/google) and the
// credential module's Google OAuth2 "connect" flow (POST
// /api/v1/credentials/oauth/google/start/).
// StorageConfig points at the public object-storage (Backblaze B2, S3-API)
// where uploaded media lives. URL is the S3 endpoint, BucketMedia the public
// bucket; internal/shared/media builds "<URL>/<BucketMedia>/<key>" from the
// bucket-relative keys the DB stores (e.g. template.image = "template/x.png").
type StorageConfig struct {
	URL         string `mapstructure:"url"`          // APP_STORAGE_URL
	BucketMedia string `mapstructure:"bucket_media"` // APP_STORAGE_BUCKET_MEDIA
}

type MonitoringConfig struct {
	PrometheusURL string `mapstructure:"prometheus_url"` // APP_MONITORING_PROMETHEUS_URL
}

type GoogleConfig struct {
	// ClientID is the expected `aud` claim on the ID token the dashboard
	// forwards — the same OAuth client ID baked into the dashboard as
	// NEXT_PUBLIC_GOOGLE_CLIENT_ID. Set via APP_GOOGLE_CLIENT_ID. Empty
	// disables the endpoint (it returns a config error instead of a panic).
	ClientID string `mapstructure:"client_id"`

	// OAuthClientID/OAuthClientSecret are the offline-access OAuth2 app
	// credentials for google_email/google_sheets/google_drive credential
	// types (core/apps/credentials/oauth_providers.py's "google" provider —
	// Django resolves these from admin-editable DB config with an env var
	// fallback; this backend only implements the env var fallback). Usually
	// the SAME OAuth client as ClientID above (Google allows one client to
	// serve both ID-token sign-in and offline authorization-code access),
	// but kept as separate config in case they diverge. Set via
	// APP_GOOGLE_OAUTH_CLIENT_ID / APP_GOOGLE_OAUTH_CLIENT_SECRET; empty
	// disables the "google" OAuth provider (start returns a config error).
	OAuthClientID     string `mapstructure:"oauth_client_id"`
	OAuthClientSecret string `mapstructure:"oauth_client_secret"`

	// OAuthCallbackBase is the public API origin used to build the OAuth2
	// redirect_uri (both the one sent to Google's authorize endpoint in
	// OAuthStart and the one resent to Google's token endpoint in the
	// callback's code exchange — they must match byte-for-byte). The path
	// is always "/api/v2/credentials/oauth/{provider}/callback/" (NOT
	// /api/v1) so a browser hitting it round-trips through the prod
	// gateway's /api/v2 -> /api/v1 rewrite and lands back on this backend.
	// Set via APP_GOOGLE_OAUTH_CALLBACK_BASE; defaults to
	// "https://api.botspace.uz" (prod) if empty.
	OAuthCallbackBase string `mapstructure:"oauth_callback_base"`

	// OAuthPanelURL is the dashboard/admin panel origin the callback's tiny
	// HTML response links back to and postMessages to (window.opener) —
	// mirrors Django's OAuthCallbackView _PANEL_URL constant. Set via
	// APP_GOOGLE_OAUTH_PANEL_URL; defaults to "https://admin.botspace.uz" if
	// empty.
	OAuthPanelURL string `mapstructure:"oauth_panel_url"`
}

// EngineConfig controls how bot engines (release/debug containers + shared
// module pods) are orchestrated. Mirrors the env vars the Django
// kubernetes_service.py/module_service.py used to read directly.
type EngineConfig struct {
	// Mode selects the Orchestrator: "k8s" (real cluster client), "stub"
	// (no-op). Empty ("auto") picks k8s when Config.Env == "production",
	// stub otherwise — set explicitly to override.
	Mode string `mapstructure:"mode"`

	Namespace          string `mapstructure:"namespace"`            // K8S_NAMESPACE in Django
	ImageRepository    string `mapstructure:"image_repository"`     // golang-engine image repo
	ImageTag           string `mapstructure:"image_tag"`            // engine_version() in Django
	RegistrySecretName string `mapstructure:"registry_secret_name"` // imagePullSecrets

	APIURL        string `mapstructure:"api_url"`        // API_URL env injected into every engine pod
	InternalToken string `mapstructure:"internal_token"` // INTERNAL_API_TOKEN — PaymentNode + debug events
	TestBotToken  string `mapstructure:"test_bot_token"` // TEST_BOT_TOKEN fallback (debug containers)

	RedisHost     string `mapstructure:"redis_host"`
	RedisPort     string `mapstructure:"redis_port"`
	RedisPassword string `mapstructure:"redis_password"`

	MongoAddr   string `mapstructure:"mongo_addr"`    // MONGO_SHARED_URL fallback MONGO_URL
	MongoDBName string `mapstructure:"mongo_db_name"` // CMS_SHARED_DB
}

// Image is the full "repo:tag" reference deployed for release/debug engines.
func (e EngineConfig) Image() string {
	return e.ImageRepository + ":" + e.ImageTag
}

// RedisAddr is "host:port", or "" when RedisHost is unset (Redis wiring
// skipped in the pod spec).
func (e EngineConfig) RedisAddr() string {
	if e.RedisHost == "" {
		return ""
	}
	port := e.RedisPort
	if port == "" {
		port = "6379"
	}
	return e.RedisHost + ":" + port
}

// OTPConfig controls phone confirmation codes. In non-prod, Default is used
// verbatim so testers don't wait for an SMS (mirrors Django OTP_DEFAULT).
type OTPConfig struct {
	Size        int           `mapstructure:"size"`         // digits, prod only
	Prod        bool          `mapstructure:"prod"`         // true -> random code + real SMS
	Default     string        `mapstructure:"default"`      // fixed dev code
	TTL         time.Duration `mapstructure:"ttl"`          // code validity window
	MaxTries    int           `mapstructure:"max_tries"`    // wrong attempts before a temporary block
	BlockFor    time.Duration `mapstructure:"block_for"`    // block duration after MaxTries
	ResendAfter time.Duration `mapstructure:"resend_after"` // cooldown between sends
}

type HTTPConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	RequestTimeout  time.Duration `mapstructure:"request_timeout"`
	MaxHeaderBytes  int           `mapstructure:"max_header_bytes"`
	MaxBodyBytes    int64         `mapstructure:"max_body_bytes"`
	CORSOrigins     []string      `mapstructure:"cors_origins"`
	RateLimitRPS    float64       `mapstructure:"rate_limit_rps"` // 0 disables rate limiting
	RateLimitBurst  int           `mapstructure:"rate_limit_burst"`
}

func (h HTTPConfig) Addr() string {
	return fmt.Sprintf("%s:%d", h.Host, h.Port)
}

type DBConfig struct {
	Host           string        `mapstructure:"host"`
	Port           int           `mapstructure:"port"`
	User           string        `mapstructure:"user"`
	Password       string        `mapstructure:"password"`
	Name           string        `mapstructure:"name"`
	SSLMode        string        `mapstructure:"ssl_mode"`
	MaxConns       int32         `mapstructure:"max_conns"`
	MinConns       int32         `mapstructure:"min_conns"`
	MaxConnLife    time.Duration `mapstructure:"max_conn_life"`
	MigrationsPath string        `mapstructure:"migrations_path"`
	AutoMigrate    bool          `mapstructure:"auto_migrate"`
}

func (d DBConfig) DSN() string {
	// url.UserPassword escapes special characters (@ : / # ?) in credentials.
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(d.User, d.Password),
		Host:     fmt.Sprintf("%s:%d", d.Host, d.Port),
		Path:     d.Name,
		RawQuery: "sslmode=" + url.QueryEscape(d.SSLMode),
	}
	return u.String()
}

func (d DBConfig) MigrationsURL() string {
	return d.DSN()
}

// JWTConfig configures RS256 token signing. Tokens are signed with the RSA
// private key and verified with the public key (asymmetric — verifiers never
// hold signing material).
type JWTConfig struct {
	PrivateKeyPath string        `mapstructure:"private_key_path"`
	PublicKeyPath  string        `mapstructure:"public_key_path"`
	AccessTTL      time.Duration `mapstructure:"access_ttl"`
	RefreshTTL     time.Duration `mapstructure:"refresh_ttl"`
	Issuer         string        `mapstructure:"issuer"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

func Load() (*Config, error) {
	// Load .env into the process environment so APP_* vars there override
	// config.yaml. godotenv does not overwrite vars already set in the real
	// environment, so precedence is: real env > .env > config.yaml > defaults.
	// A missing .env file is fine.
	_ = godotenv.Load()

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./configs")
	v.AddConfigPath(".")

	setDefaults(v)

	v.SetEnvPrefix("APP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// AutomaticEnv doesn't reliably bind nested keys during Unmarshal unless the
	// key is explicitly bound, so env-only secrets (no config.yaml value) get
	// lost. Bind them explicitly. (viper gotcha: nested struct + AutomaticEnv.)
	for _, key := range []string{
		"google.client_id", "google.oauth_client_id", "google.oauth_client_secret",
		"google.oauth_callback_base", "google.oauth_panel_url",
		"crypto.key", "internal.token", "mongo.url",
		"engine.mode", "engine.image_repository", "engine.image_tag",
		"engine.internal_token", "engine.redis_host", "engine.redis_port", "engine.redis_password",
		"engine.test_bot_token", "engine.mongo_addr", "engine.mongo_db_name",
		"engine.api_url",
		"storage.url", "storage.bucket_media",
		"monitoring.prometheus_url",
	} {
		_ = v.BindEnv(key)
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.Engine.InternalToken == "" {
		cfg.Engine.InternalToken = v.GetString("internal.token")
	}
	if cfg.Engine.MongoAddr == "" {
		cfg.Engine.MongoAddr = v.GetString("mongo.url")
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.JWT.PrivateKeyPath == "" || c.JWT.PublicKeyPath == "" {
		return fmt.Errorf("jwt.private_key_path and jwt.public_key_path are required")
	}
	if c.DB.Host == "" {
		return fmt.Errorf("db.host required")
	}
	return nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("env", "development")

	v.SetDefault("http.host", "0.0.0.0")
	v.SetDefault("http.port", 8080)
	v.SetDefault("http.read_timeout", "15s")
	v.SetDefault("http.write_timeout", "15s")
	v.SetDefault("http.idle_timeout", "60s")
	v.SetDefault("http.shutdown_timeout", "10s")
	v.SetDefault("http.request_timeout", "30s")
	v.SetDefault("http.max_header_bytes", 1<<20) // 1 MiB
	v.SetDefault("http.max_body_bytes", 1<<20)   // 1 MiB
	v.SetDefault("http.cors_origins", []string{"*"})
	v.SetDefault("http.rate_limit_rps", 0) // disabled by default
	v.SetDefault("http.rate_limit_burst", 20)

	v.SetDefault("db.host", "localhost")
	v.SetDefault("db.port", 5432)
	v.SetDefault("db.user", "postgres")
	v.SetDefault("db.password", "postgres")
	v.SetDefault("db.name", "jstgo")
	v.SetDefault("db.ssl_mode", "disable")
	v.SetDefault("db.max_conns", 20)
	v.SetDefault("db.min_conns", 2)
	v.SetDefault("db.max_conn_life", "30m")
	v.SetDefault("db.migrations_path", "file://migrations")
	v.SetDefault("db.auto_migrate", true)

	v.SetDefault("jwt.private_key_path", "keys/jwt_private.pem")
	v.SetDefault("jwt.public_key_path", "keys/jwt_public.pem")
	v.SetDefault("jwt.access_ttl", "15m")
	v.SetDefault("jwt.refresh_ttl", "168h")
	v.SetDefault("jwt.issuer", "botspace")

	v.SetDefault("otp.size", 4)
	v.SetDefault("otp.prod", false)
	v.SetDefault("otp.default", "1111")
	v.SetDefault("otp.ttl", "2m")
	v.SetDefault("otp.max_tries", 3)
	v.SetDefault("otp.block_for", "5m")
	v.SetDefault("otp.resend_after", "60s")

	v.SetDefault("log.level", "info")

	v.SetDefault("google.client_id", "")                                  // APP_GOOGLE_CLIENT_ID — unset disables /auth/google
	v.SetDefault("google.oauth_client_id", "")                            // APP_GOOGLE_OAUTH_CLIENT_ID — unset disables credential oauth "google" provider
	v.SetDefault("google.oauth_client_secret", "")                        // APP_GOOGLE_OAUTH_CLIENT_SECRET
	v.SetDefault("google.oauth_callback_base", "https://api.botspace.uz") // APP_GOOGLE_OAUTH_CALLBACK_BASE
	v.SetDefault("google.oauth_panel_url", "https://admin.botspace.uz")   // APP_GOOGLE_OAUTH_PANEL_URL

	v.SetDefault("engine.mode", "") // "" = auto (k8s in production, stub otherwise)
	v.SetDefault("engine.namespace", "botmother")
	v.SetDefault("engine.image_repository", "registry.iprogrammer.uz/botspace/golang-engine")
	// The live cluster's engine tag moves independently of this backend (it
	// was e.g. "3.5.87-prod" at the time of writing — Django tracked it in an
	// admin-editable DB setting, engine_version()). "latest" here is only a
	// safe fallback; set APP_ENGINE_IMAGE_TAG in the deploy env to whatever
	// tag is currently promoted so releases don't pin to a stale/missing one.
	v.SetDefault("engine.image_tag", "latest")
	v.SetDefault("engine.registry_secret_name", "registry-secret")
	v.SetDefault("engine.redis_host", "redis")
	v.SetDefault("engine.redis_port", "6379")
	v.SetDefault("engine.mongo_db_name", "botspace_cms")

	v.SetDefault("monitoring.prometheus_url", "http://monitoring-kube-prometheus-prometheus.monitoring.svc:9090")
}
