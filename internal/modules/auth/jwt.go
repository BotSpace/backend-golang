package auth

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/botspace/backend/internal/shared/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenIssuer creates and verifies RS256 access/refresh tokens. It signs with
// the RSA private key and verifies with the public key. It implements
// middleware.TokenVerifier.
type TokenIssuer struct {
	priv *rsa.PrivateKey
	pub  *rsa.PublicKey
	cfg  config.JWTConfig
}

// NewTokenIssuer builds an issuer from a parsed RSA key pair. Use LoadKeys to
// read the pair from the configured PEM files.
func NewTokenIssuer(priv *rsa.PrivateKey, pub *rsa.PublicKey, cfg config.JWTConfig) *TokenIssuer {
	return &TokenIssuer{priv: priv, pub: pub, cfg: cfg}
}

// Claims mirrors Django SIMPLE_JWT's claim shape so tokens are interchangeable
// between the Go and Django issuers for the lifetime of the migration: other
// services (agent, webhook, backend worker) that decode Django-issued tokens
// read `user_id` and `token_type` (config/conf/jwt.py: USER_ID_CLAIM,
// TOKEN_TYPE_CLAIM). `sub`/`jti`/`iat`/`exp` come from jwt.RegisteredClaims
// and already match (JTI_CLAIM="jti" on the Django side too).
type Claims struct {
	jwt.RegisteredClaims
	UserID    string `json:"user_id"`
	TokenType string `json:"token_type"`
	Role      string `json:"role,omitempty"`
}

// identity returns the token's user identifier, preferring the Django-style
// user_id claim and falling back to sub for tokens that only set one (e.g.
// the reset token, which carries a phone number in sub and has no user_id).
func (c *Claims) identity() string {
	if c.UserID != "" {
		return c.UserID
	}
	return c.Subject
}

const (
	typeAccess  = "access"
	typeRefresh = "refresh"
	typeReset   = "reset"
)

// NewAccessToken issues a short-lived access token carrying the user's role.
func (t *TokenIssuer) NewAccessToken(userID uuid.UUID, role string) (string, time.Time, error) {
	signed, _, exp, err := t.sign(userID, typeAccess, role, t.cfg.AccessTTL)
	return signed, exp, err
}

// NewRefreshToken issues a refresh token and returns its jti so the caller can
// persist it for revocation/rotation.
func (t *TokenIssuer) NewRefreshToken(userID uuid.UUID) (token string, jti uuid.UUID, exp time.Time, err error) {
	return t.sign(userID, typeRefresh, "", t.cfg.RefreshTTL)
}

// NewResetToken issues a short-lived token proving OTP-verified ownership of a
// phone, consumed by the password-reset "set" step. The phone is the subject.
func (t *TokenIssuer) NewResetToken(phone string, ttl time.Duration) (string, error) {
	expiresAt := time.Now().Add(ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   phone,
			Issuer:    t.cfg.Issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		TokenType: typeReset,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(t.priv)
	if err != nil {
		return "", fmt.Errorf("sign reset token: %w", err)
	}
	return signed, nil
}

// VerifyResetToken returns the phone a valid reset token was issued for.
func (t *TokenIssuer) VerifyResetToken(token string) (phone string, err error) {
	claims, err := t.verify(token, typeReset)
	if err != nil {
		return "", err
	}
	return claims.Subject, nil
}

func (t *TokenIssuer) sign(userID uuid.UUID, kind, role string, ttl time.Duration) (string, uuid.UUID, time.Time, error) {
	expiresAt := time.Now().Add(ttl)
	jti := uuid.New()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti.String(),
			Subject:   userID.String(),
			Issuer:    t.cfg.Issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		UserID:    userID.String(),
		TokenType: kind,
		Role:      role,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(t.priv)
	if err != nil {
		return "", uuid.Nil, time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, jti, expiresAt, nil
}

// VerifyAccessToken implements middleware.TokenVerifier, returning the user id
// and role encoded in the token.
func (t *TokenIssuer) VerifyAccessToken(token string) (userID, role string, err error) {
	claims, err := t.verify(token, typeAccess)
	if err != nil {
		return "", "", err
	}
	return claims.identity(), claims.Role, nil
}

// VerifyRefreshToken returns the user id and jti of a valid refresh token.
func (t *TokenIssuer) VerifyRefreshToken(token string) (userID string, jti uuid.UUID, err error) {
	claims, err := t.verify(token, typeRefresh)
	if err != nil {
		return "", uuid.Nil, err
	}
	id, err := uuid.Parse(claims.ID)
	if err != nil {
		return "", uuid.Nil, errors.New("missing token id")
	}
	return claims.identity(), id, nil
}

func (t *TokenIssuer) verify(token, expectedType string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(tk *jwt.Token) (any, error) {
		if _, ok := tk.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", tk.Header["alg"])
		}
		return t.pub, nil
	}, jwt.WithIssuer(t.cfg.Issuer), jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.TokenType != expectedType {
		return nil, errors.New("wrong token type")
	}
	if claims.identity() == "" {
		return nil, errors.New("missing subject")
	}
	return claims, nil
}
