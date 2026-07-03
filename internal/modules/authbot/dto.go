package authbot

import "time"

// IssueCodeRequest is submitted by the Telegram bot service after a user
// shares their phone number (Django: handlers.auth.phone_handler). It is not
// user-facing — only the bot service (which holds the phone/contact data
// straight from Telegram) calls this endpoint.
type IssueCodeRequest struct {
	TelegramID int64  `json:"telegram_id" validate:"required"`
	Phone      string `json:"phone" validate:"required,min=5,max=32"`
	FirstName  string `json:"first_name" validate:"max=1000"`
	LastName   string `json:"last_name" validate:"max=1000"`
	Username   string `json:"username" validate:"max=255"`
}

// IssueCodeResponse carries what the bot needs to render the confirmation
// message (Django: messages.you_otp.format(code.code)).
type IssueCodeResponse struct {
	CodeID    string    `json:"code_id"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

func toIssueCodeResponse(issued IssuedCode) IssueCodeResponse {
	return IssueCodeResponse{
		CodeID:    issued.CodeID.String(),
		Code:      issued.Value,
		ExpiresAt: issued.ExpiresAt,
	}
}

// SetMessageIDRequest lets the bot service record which Telegram message
// shows the code (Django: code.message_id = sent_message.id), so it can be
// edited/deleted later.
type SetMessageIDRequest struct {
	MessageID int64 `json:"message_id" validate:"required"`
}

// LoginRequest exchanges a one-time code for app JWT tokens (Django:
// LoginSerializer — code is a 6-digit integer between 100000 and 999999).
type LoginRequest struct {
	Code string `json:"code" validate:"required,len=6,numeric"`
}

// UserSummary is the minimal user payload embedded in LoginResponse (mirrors
// Django's UserSerializer(user).data under the "user" key).
type UserSummary struct {
	ID        string `json:"id"`
	Phone     string `json:"phone"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Role      string `json:"role"`
}

// TokenResponse mirrors auth.TokenResponse's wire shape exactly, so a client
// handles it identically regardless of which endpoint issued it.
type TokenResponse struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	TokenType        string    `json:"token_type"`
}

// LoginResponse mirrors Django's {"user": ..., "token": ...} LoginView
// payload shape.
type LoginResponse struct {
	User  UserSummary   `json:"user"`
	Token TokenResponse `json:"token"`
}

func toLoginResponse(r LoginResult) LoginResponse {
	return LoginResponse{
		User: UserSummary{
			ID:        r.UserID.String(),
			Phone:     r.Phone,
			FirstName: r.FirstName,
			LastName:  r.LastName,
			Role:      r.Role,
		},
		Token: TokenResponse{
			AccessToken:      r.Tokens.AccessToken,
			RefreshToken:     r.Tokens.RefreshToken,
			AccessExpiresAt:  r.Tokens.AccessExpiresAt,
			RefreshExpiresAt: r.Tokens.RefreshExpiresAt,
			TokenType:        r.Tokens.TokenType,
		},
	}
}
