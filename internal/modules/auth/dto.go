package auth

import "time"

// RegisterRequest starts registration: create a pending user + send an OTP.
type RegisterRequest struct {
	Phone     string `json:"phone" validate:"required,min=5,max=32"`
	FirstName string `json:"first_name" validate:"required,min=1,max=150"`
	LastName  string `json:"last_name" validate:"required,min=1,max=150"`
	Password  string `json:"password" validate:"required,min=8,max=72"` // bcrypt truncates past 72 bytes
}

// ConfirmRequest finishes registration (or any OTP step) by phone + code.
type ConfirmRequest struct {
	Phone string `json:"phone" validate:"required"`
	Code  string `json:"code" validate:"required"`
}

// PhoneRequest is used to (re)send an OTP: registration resend, reset start.
type PhoneRequest struct {
	Phone string `json:"phone" validate:"required,min=5,max=32"`
}

// LoginRequest authenticates a confirmed user by phone + password.
type LoginRequest struct {
	Phone    string `json:"phone" validate:"required"`
	Password string `json:"password" validate:"required"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// GoogleLoginRequest carries the ID token a Google Identity Services button
// hands the frontend (dashboard: POST /api/v1/auth/google -> here).
type GoogleLoginRequest struct {
	IDToken string `json:"id_token" validate:"required"`
}

// ResetSetRequest sets a new password using the reset token from confirm.
type ResetSetRequest struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=8,max=72"`
}

// MessageResponse is a simple {detail: "..."} acknowledgement (OTP sent, etc.).
type MessageResponse struct {
	Detail string `json:"detail"`
}

// ResetTokenResponse carries the short-lived token that authorises the "set"
// step of a password reset.
type ResetTokenResponse struct {
	Token string `json:"token"`
}

type TokenResponse struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	TokenType        string    `json:"token_type"`
}

// ---- Personal access tokens (MCP clients) --------------------------------

// CreateAPITokenRequest is POST /auth/api-tokens/'s body.
type CreateAPITokenRequest struct {
	Name string `json:"name" validate:"omitempty,max=100"`
}

// APITokenResponse is the owner-facing listing shape — never carries the raw
// token (only CreateAPITokenResponse does, once, at creation).
type APITokenResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toAPITokenResponse(p *PersonalAccessToken) APITokenResponse {
	return APITokenResponse{
		ID: p.ID.String(), Name: p.Name, Prefix: p.Prefix,
		LastUsedAt: p.LastUsedAt, CreatedAt: p.CreatedAt,
	}
}

func toAPITokenListResponse(items []PersonalAccessToken) []APITokenResponse {
	out := make([]APITokenResponse, len(items))
	for i := range items {
		out[i] = toAPITokenResponse(&items[i])
	}
	return out
}

// CreateAPITokenResponse mirrors APITokenViewSet.create's 201 body — the
// only response that ever carries the raw token.
type CreateAPITokenResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Token  string `json:"token"`
	Prefix string `json:"prefix"`
}

// TokenExchangeRequest is POST /auth/token/exchange/'s body.
type TokenExchangeRequest struct {
	Token string `json:"token" validate:"required"`
}

// ExchangeResponse mirrors TokenExchangeView's simplejwt-shaped
// {"access", "refresh"} body exactly — deliberately NOT TokenResponse's
// "access_token"/"refresh_token" naming, since agent/src/agent/mcp/auth.py's
// _exchange_bsp() reads `data.get("access")`/`data.get("refresh")`.
type ExchangeResponse struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
}

func toExchangeResponse(t TokenResponse) ExchangeResponse {
	return ExchangeResponse{Access: t.AccessToken, Refresh: t.RefreshToken}
}
