package logger

import (
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger = zap.Logger

type Field = zap.Field

func New(env, level string) (*Logger, error) {
	var cfg zap.Config
	if env == "production" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}
	cfg.Level = zap.NewAtomicLevelAt(lvl)

	// The development config attaches a stack trace at Warn+, which spams every
	// 4xx request log with a useless trace pointing at the logger middleware.
	// Only attach traces at Error+ (panics already log debug.Stack themselves).
	return cfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
}

func String(k, v string) Field                 { return zap.String(k, v) }
func Int(k string, v int) Field                { return zap.Int(k, v) }
func Int64(k string, v int64) Field            { return zap.Int64(k, v) }
func Bool(k string, v bool) Field              { return zap.Bool(k, v) }
func Any(k string, v any) Field                { return zap.Any(k, v) }
func Err(err error) Field                      { return zap.Error(err) }
func Duration(k string, v time.Duration) Field { return zap.Duration(k, v) }
