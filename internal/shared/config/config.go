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
	Env  string     `mapstructure:"env"`
	HTTP HTTPConfig `mapstructure:"http"`
	DB   DBConfig   `mapstructure:"db"`
	JWT  JWTConfig  `mapstructure:"jwt"`
	OTP  OTPConfig  `mapstructure:"otp"`
	Log  LogConfig  `mapstructure:"log"`
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
}
