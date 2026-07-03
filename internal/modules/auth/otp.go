package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/config"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sender delivers an OTP code to a phone. Swap the dev logger for an Eskiz.uz
// SMS client in production.
type Sender interface {
	Send(ctx context.Context, phone, code string) error
}

// LogSender writes the code to the log instead of sending an SMS — for dev.
type LogSender struct{ log *logger.Logger }

func NewLogSender(log *logger.Logger) *LogSender { return &LogSender{log: log} }

func (s *LogSender) Send(_ context.Context, phone, code string) error {
	s.log.Info("otp code (dev)", logger.String("phone", phone), logger.String("code", code))
	return nil
}

// OTPService issues and verifies phone confirmation codes, backed by the
// sms_confirm table. It mirrors the Django SmsService semantics: resend
// cooldown, expiry, and a max-tries block.
type OTPService struct {
	q      *sqlcdb.Queries
	sender Sender
	cfg    config.OTPConfig
}

func NewOTPService(pool *pgxpool.Pool, sender Sender, cfg config.OTPConfig) *OTPService {
	return &OTPService{q: sqlcdb.New(pool), sender: sender, cfg: cfg}
}

// SendConfirm generates a code, stores it, and dispatches it. It enforces the
// resend cooldown: a still-locked phone is rejected with 429.
func (s *OTPService) SendConfirm(ctx context.Context, phone string) error {
	if existing, err := s.q.GetSmsConfirm(ctx, phone); err == nil {
		if existing.ResendUnlockTime != nil && time.Now().Before(*existing.ResendUnlockTime) {
			wait := time.Until(*existing.ResendUnlockTime).Round(time.Second)
			return httpx.TooManyRequests("otp.resend_blocked",
				fmt.Sprintf("please wait %s before requesting a new code", wait))
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return httpx.Internal(err)
	}

	code := s.generateCode()
	now := time.Now()
	resendUnlock := now.Add(s.cfg.ResendAfter)
	if _, err := s.q.UpsertSmsConfirm(ctx, sqlcdb.UpsertSmsConfirmParams{
		Phone:            phone,
		Code:             code,
		ExpiredTime:      now.Add(s.cfg.TTL),
		ResendUnlockTime: &resendUnlock,
	}); err != nil {
		return httpx.Internal(err)
	}

	if err := s.sender.Send(ctx, phone, code); err != nil {
		return httpx.Internal(fmt.Errorf("send otp: %w", err))
	}
	return nil
}

// CheckConfirm validates a submitted code. On success the row is deleted so a
// code cannot be replayed. Wrong codes increment try_count and, past the
// limit, block the phone temporarily.
func (s *OTPService) CheckConfirm(ctx context.Context, phone, code string) error {
	row, err := s.q.GetSmsConfirm(ctx, phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.BadRequest("otp.invalid", "invalid confirmation code")
	}
	if err != nil {
		return httpx.Internal(err)
	}

	if time.Now().After(row.ExpiredTime) {
		return httpx.BadRequest("otp.expired", "confirmation code has expired")
	}
	if row.UnlockTime != nil && time.Now().Before(*row.UnlockTime) {
		wait := time.Until(*row.UnlockTime).Round(time.Second)
		return httpx.TooManyRequests("otp.blocked", fmt.Sprintf("too many attempts, try again in %s", wait))
	}

	if subtleEqual(row.Code, code) {
		if err := s.q.DeleteSmsConfirm(ctx, phone); err != nil {
			return httpx.Internal(err)
		}
		return nil
	}

	// Wrong code: count the attempt and block once the limit is reached.
	var unlock *time.Time
	if int(row.TryCount)+1 >= s.cfg.MaxTries {
		t := time.Now().Add(s.cfg.BlockFor)
		unlock = &t
	}
	if _, err := s.q.IncrSmsConfirmTry(ctx, sqlcdb.IncrSmsConfirmTryParams{Phone: phone, UnlockTime: unlock}); err != nil {
		return httpx.Internal(err)
	}
	return httpx.BadRequest("otp.invalid", "invalid confirmation code")
}

// generateCode returns a random numeric code in prod, or the fixed dev code.
func (s *OTPService) generateCode() string {
	if !s.cfg.Prod {
		return s.cfg.Default
	}
	var b strings.Builder
	for i := 0; i < s.cfg.Size; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			// crypto/rand failure is unrecoverable here; fall back to the dev code.
			return s.cfg.Default
		}
		b.WriteString(n.String())
	}
	return b.String()
}

// subtleEqual compares codes in constant time to avoid leaking length/prefix.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
