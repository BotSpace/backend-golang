package authbot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

const (
	codeDigits = 6
	// maxCodeRetries mirrors Django's @retry(stop_after_attempt(10)) on
	// CodeManager.get_or_create_code.
	maxCodeRetries = 10
	// passwordBytes sizes the random, never-exposed password given to
	// accounts created via Telegram login (see resolveUser).
	passwordBytes = 32
)

// Usecase orchestrates the Telegram phone-share -> one-time-code -> app-JWT
// flow (Django: handlers.auth.phone_handler + views.authbot.LoginView). It
// composes the authbot repository, the user module's usecase (to
// resolve/create the app user), and an injected TokenIssuer.
type Usecase struct {
	repo   Repository
	users  user.Usecase
	tokens TokenIssuer
}

func NewUsecase(repo Repository, users user.Usecase, tokens TokenIssuer) *Usecase {
	return &Usecase{repo: repo, users: users, tokens: tokens}
}

// IssueCode registers/refreshes the Telegram identity and returns a fresh (or
// still-valid) login code. Called by the Telegram bot service after a user
// shares their phone number (Django: phone_handler).
func (u *Usecase) IssueCode(ctx context.Context, in IssueCodeInput) (IssuedCode, error) {
	if in.Phone == "" {
		return IssuedCode{}, httpx.BadRequest("authbot.phone_required", "phone is required")
	}
	if in.TelegramID == 0 {
		return IssuedCode{}, httpx.BadRequest("authbot.telegram_id_required", "telegram_id is required")
	}

	botUser, err := u.repo.UpsertBotUser(ctx, in)
	if err != nil {
		return IssuedCode{}, httpx.Internal(err)
	}

	var code *Code
	for attempt := 0; attempt < maxCodeRetries; attempt++ {
		value, genErr := generateNumericCode(codeDigits)
		if genErr != nil {
			return IssuedCode{}, httpx.Internal(genErr)
		}
		code, err = u.repo.UpsertCode(ctx, botUser.ID, value, time.Now().Add(CodeTTL))
		if err == nil {
			break
		}
		if errors.Is(err, ErrCodeCollision) {
			code = nil
			continue
		}
		return IssuedCode{}, httpx.Internal(err)
	}
	if code == nil {
		return IssuedCode{}, httpx.Internal(fmt.Errorf("authbot: exhausted %d code generation attempts", maxCodeRetries))
	}

	return IssuedCode{CodeID: code.ID, BotUserID: botUser.ID, Value: code.Value, ExpiresAt: code.ExpiresAt}, nil
}

// SetMessageID records which Telegram message displays a code, so the bot
// can edit/delete it later (Django: code.message_id = sent_message.id).
func (u *Usecase) SetMessageID(ctx context.Context, codeID uuid.UUID, messageID int64) error {
	if err := u.repo.SetCodeMessageID(ctx, codeID, messageID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("authbot.code_not_found", "code not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

// Login exchanges a one-time code for app JWT tokens (Django: LoginView).
// The code is consumed (deleted) on success so it cannot be replayed.
func (u *Usecase) Login(ctx context.Context, code string) (LoginResult, error) {
	found, botUser, err := u.repo.GetCodeWithBotUser(ctx, code)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return LoginResult{}, httpx.BadRequest("authbot.invalid_code", "invalid otp code")
		}
		return LoginResult{}, httpx.Internal(err)
	}
	if found.Expired() {
		_ = u.repo.DeleteCode(ctx, found.ID) // best-effort cleanup of a stale code
		return LoginResult{}, httpx.BadRequest("authbot.code_expired", "otp code has expired")
	}

	usr, err := u.resolveUser(ctx, botUser)
	if err != nil {
		return LoginResult{}, err
	}

	tokens, err := u.tokens.IssueTokens(ctx, usr.ID, usr.Role)
	if err != nil {
		return LoginResult{}, httpx.Internal(err)
	}

	if err := u.repo.DeleteCode(ctx, found.ID); err != nil {
		return LoginResult{}, httpx.Internal(err)
	}

	return LoginResult{
		UserID:     usr.ID,
		Phone:      usr.Phone,
		FirstName:  usr.FirstName,
		LastName:   usr.LastName,
		Role:       usr.Role,
		Tokens:     tokens,
		TelegramID: botUser.TelegramID,
		MessageID:  found.MessageID,
	}, nil
}

// resolveUser mirrors Django's User.objects.get_or_create(phone=...,
// defaults=...) followed by UserService.validate_user (which sets
// validated_at if it was unset). A brand-new user gets a random,
// never-exposed password: the Telegram contact share already proved phone
// ownership, so no OTP step is needed here; the user can set a real password
// later via the normal password-reset flow.
func (u *Usecase) resolveUser(ctx context.Context, bu *BotUser) (*user.User, error) {
	existing, err := u.users.GetByPhone(ctx, bu.Phone)
	if err == nil {
		if existing.IsValidated() {
			return existing, nil
		}
		return u.users.MarkValidated(ctx, existing.ID)
	}
	if ae, ok := httpx.AsAppError(err); !ok || ae.Status != http.StatusNotFound {
		return nil, err // already an httpx-wrapped error (e.g. internal)
	}

	password, genErr := randomToken(passwordBytes)
	if genErr != nil {
		return nil, httpx.Internal(genErr)
	}
	created, err := u.users.Register(ctx, user.RegisterInput{
		Phone:     bu.Phone,
		FirstName: bu.FirstName,
		LastName:  bu.LastName,
		Password:  password,
	})
	if err != nil {
		return nil, err
	}
	return u.users.MarkValidated(ctx, created.ID)
}

// generateNumericCode returns a cryptographically random n-digit numeric
// string with no leading zero, mirroring Django's
// random.randint(100000, 999999) for n=6.
func generateNumericCode(digits int) (string, error) {
	if digits < 1 {
		digits = codeDigits
	}
	min := int64(1)
	for i := 1; i < digits; i++ {
		min *= 10
	}
	span := min*10 - min // e.g. 900000 possible values in [100000, 999999]
	n, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	return strconv.FormatInt(min+n.Int64(), 10), nil
}

// randomToken returns a hex-encoded random secret used as an unguessable
// password for accounts created via Telegram login.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
