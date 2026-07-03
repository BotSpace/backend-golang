// Package authbot ports Django's authbot app: a Telegram bot that collects a
// user's phone number, issues a one-time login code, and lets the frontend
// exchange that code for app JWTs. It is mounted at /authbot (not /api/v1) to
// match the Django URL layout.
//
// This module owns persistence only for the Telegram-side identity (BotUser)
// and the one-time code (Code). It resolves/creates the app user via the
// user module's Usecase (same pattern auth/usecase.go already uses) and mints
// tokens through the TokenIssuer port below, which the server wiring
// implements with an adapter around auth.TokenIssuer — this package never
// imports the auth package directly.
package authbot

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrCodeCollision is returned by Repository.UpsertCode when the randomly
// generated code value collides with another bot user's still-active code
// (the `code` column is globally unique). The usecase regenerates the value
// and retries, mirroring Django's @retry(stop_after_attempt(10)) on
// CodeManager.get_or_create_code.
var ErrCodeCollision = errors.New("authbot: code collision")

// CodeTTL bounds how long an issued login code stays valid. The Django bot
// message promised "Amal qilish muddati: 10 daqiqa" but the model/serializer
// never actually enforced it (Code had no expiry column) — we do.
const CodeTTL = 10 * time.Minute

// BotUser is a Telegram identity that proved phone ownership by sharing its
// contact with the bot (Django: authbot.BotUser). It is deliberately
// separate from user.User: a BotUser only proves phone ownership over
// Telegram; Login resolves/creates the corresponding app user by phone.
type BotUser struct {
	ID         uuid.UUID
	TelegramID int64
	Phone      string
	FirstName  string
	LastName   string
	Username   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Code is a one-time login code tied to a BotUser (Django: authbot.Code, a
// OneToOneField to BotUser — one active code per bot user). MessageID is the
// Telegram message that displayed the code, recorded so the bot can edit or
// delete it once the code is consumed.
type Code struct {
	ID        uuid.UUID
	Value     string
	MessageID *int64
	BotUserID uuid.UUID
	ExpiresAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Expired reports whether the code is past its validity window.
func (c *Code) Expired() bool { return time.Now().After(c.ExpiresAt) }

// IssueCodeInput is the Telegram contact-share payload forwarded by the bot
// service after a user taps "Send phone number" (Django:
// handlers.auth.phone_handler).
type IssueCodeInput struct {
	TelegramID int64
	Phone      string
	FirstName  string
	LastName   string
	Username   string
}

// IssuedCode is what the bot needs to render/track the confirmation message.
type IssuedCode struct {
	CodeID    uuid.UUID
	BotUserID uuid.UUID
	Value     string
	ExpiresAt time.Time
}

// LoginResult is what a successful code exchange yields: the resolved app
// user id/phone/name/role, its fresh tokens, and enough Telegram context
// (TelegramID/MessageID) for the caller to update the original bot message
// if it wants to (Django's LoginView edits that message in-process; this
// module has no Telegram client, so it only reports the coordinates).
type LoginResult struct {
	UserID     uuid.UUID
	Phone      string
	FirstName  string
	LastName   string
	Role       string
	Tokens     TokenPair
	TelegramID int64
	MessageID  *int64
}

// TokenPair mirrors auth.TokenResponse's wire shape so /authbot/login stays
// wire-compatible with /api/v1/auth/* responses.
type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	TokenType        string
}

// TokenIssuer mints app JWTs for a resolved user. Implemented in server
// wiring by an adapter around *auth.TokenIssuer — kept as an interface here
// so this module never imports the auth package (mirrors why
// middleware.TokenVerifier exists).
type TokenIssuer interface {
	IssueTokens(ctx context.Context, userID uuid.UUID, role string) (TokenPair, error)
}

// Repository is the persistence port for BotUser/Code. The usecase depends
// only on this interface.
type Repository interface {
	// UpsertBotUser registers/refreshes the Telegram identity — mirrors
	// BotUser.objects.get_or_create(user_id=..., phone=..., defaults=...).
	UpsertBotUser(ctx context.Context, in IssueCodeInput) (*BotUser, error)
	// UpsertCode returns the still-valid code for a bot user or mints a fresh
	// one if none exists or the previous one expired — mirrors
	// CodeManager.get_or_create_code, plus expiry. codeValue/expiresAt are the
	// candidate values to use if a new code is needed; the caller regenerates
	// codeValue and retries on ErrCodeCollision.
	UpsertCode(ctx context.Context, botUserID uuid.UUID, codeValue string, expiresAt time.Time) (*Code, error)
	// SetCodeMessageID records which Telegram message shows the code.
	SetCodeMessageID(ctx context.Context, codeID uuid.UUID, messageID int64) error
	// GetCodeWithBotUser looks up a code by its numeric value together with
	// its BotUser — mirrors LoginSerializer.validate_code + code.user.
	GetCodeWithBotUser(ctx context.Context, value string) (*Code, *BotUser, error)
	// DeleteCode consumes (deletes) a code so it cannot be replayed.
	DeleteCode(ctx context.Context, codeID uuid.UUID) error
	// DeleteExpiredCodes purges codes nobody ever redeemed — see
	// Usecase.CleanupExpiredCodes.
	DeleteExpiredCodes(ctx context.Context) (int64, error)
}
