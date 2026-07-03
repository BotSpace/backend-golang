package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// resetTokenTTL bounds how long a password-reset "set" step stays valid after
// OTP confirmation.
const resetTokenTTL = 10 * time.Minute

// Usecase orchestrates the phone/OTP auth flow by composing the user usecase,
// the token issuer, an OTP service, and a refresh-token store.
type Usecase struct {
	users  user.Usecase
	tokens *TokenIssuer
	otp    *OTPService
	store  RefreshStore
	// google is nil when APP_GOOGLE_CLIENT_ID is unset — GoogleLogin then
	// fails with a clear config error instead of a nil-pointer panic.
	google GoogleVerifier
}

func NewUsecase(users user.Usecase, tokens *TokenIssuer, otp *OTPService, store RefreshStore, google GoogleVerifier) *Usecase {
	return &Usecase{users: users, tokens: tokens, otp: otp, store: store, google: google}
}

// Register creates a pending user and sends the confirmation code. The user
// cannot log in until they confirm (Authenticate rejects unvalidated users).
func (u *Usecase) Register(ctx context.Context, in RegisterRequest) error {
	if _, err := u.users.Register(ctx, user.RegisterInput{
		Phone:     in.Phone,
		FirstName: in.FirstName,
		LastName:  in.LastName,
		Password:  in.Password,
	}); err != nil {
		return err
	}
	return u.otp.SendConfirm(ctx, in.Phone)
}

// Resend re-sends the confirmation code (subject to the resend cooldown).
func (u *Usecase) Resend(ctx context.Context, phone string) error {
	return u.otp.SendConfirm(ctx, phone)
}

// Confirm verifies the OTP, marks the user validated, and issues tokens.
func (u *Usecase) Confirm(ctx context.Context, in ConfirmRequest) (TokenResponse, error) {
	if err := u.otp.CheckConfirm(ctx, in.Phone, in.Code); err != nil {
		return TokenResponse{}, err
	}
	usr, err := u.users.GetByPhone(ctx, in.Phone)
	if err != nil {
		return TokenResponse{}, err
	}
	validated, err := u.users.MarkValidated(ctx, usr.ID)
	if err != nil {
		return TokenResponse{}, err
	}
	return u.issueTokens(ctx, validated.ID, validated.Role)
}

// Login authenticates a confirmed user by phone + password.
func (u *Usecase) Login(ctx context.Context, in LoginRequest) (TokenResponse, error) {
	found, err := u.users.Authenticate(ctx, in.Phone, in.Password)
	if err != nil {
		return TokenResponse{}, err
	}
	return u.issueTokens(ctx, found.ID, found.Role)
}

// GoogleLogin verifies a Google ID token (from Google Identity Services on
// the dashboard), gets-or-creates the matching user by verified email, and
// issues the same token pair as the phone/password Login flow.
func (u *Usecase) GoogleLogin(ctx context.Context, in GoogleLoginRequest) (TokenResponse, error) {
	if u.google == nil {
		return TokenResponse{}, httpx.Internal(fmt.Errorf("google sign-in not configured: APP_GOOGLE_CLIENT_ID unset"))
	}
	claims, err := u.google.Verify(ctx, in.IDToken)
	if err != nil {
		return TokenResponse{}, httpx.Unauthorized("auth.invalid_google_token", err.Error())
	}
	if !claims.EmailVerified {
		return TokenResponse{}, httpx.Forbidden("auth.google_email_unverified", "google account email is not verified")
	}

	usr, err := u.users.GetByEmail(ctx, claims.Email)
	if err != nil {
		ae, ok := httpx.AsAppError(err)
		if !ok || ae.Status != http.StatusNotFound {
			return TokenResponse{}, err
		}
		usr, err = u.createGoogleUser(ctx, claims)
		if err != nil {
			return TokenResponse{}, err
		}
	}
	return u.issueTokens(ctx, usr.ID, usr.Role)
}

// createGoogleUser provisions a new account for a first-time Google sign-in.
// It has no phone and an unusable random password — Google is the only way
// in until the user sets one up via the profile/password flows — and is
// marked validated immediately since Google already verified the email.
func (u *Usecase) createGoogleUser(ctx context.Context, claims GoogleClaims) (*user.User, error) {
	randPassword := make([]byte, 32)
	if _, err := rand.Read(randPassword); err != nil {
		return nil, httpx.Internal(fmt.Errorf("generate random password: %w", err))
	}
	firstName := claims.FirstName
	if firstName == "" {
		firstName = claims.Email
	}
	created, err := u.users.Register(ctx, user.RegisterInput{
		FirstName: firstName,
		LastName:  claims.LastName,
		Password:  hex.EncodeToString(randPassword),
	})
	if err != nil {
		return nil, err
	}
	email := claims.Email
	if _, err := u.users.UpdateProfile(ctx, created.ID, user.ProfileUpdate{Email: &email}); err != nil {
		return nil, err
	}
	validated, err := u.users.MarkValidated(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	return validated, nil
}

// ResetStart sends an OTP to a known phone so its owner can reset the password.
func (u *Usecase) ResetStart(ctx context.Context, phone string) error {
	if _, err := u.users.GetByPhone(ctx, phone); err != nil {
		return err
	}
	return u.otp.SendConfirm(ctx, phone)
}

// ResetConfirm verifies the OTP and returns a short-lived reset token.
func (u *Usecase) ResetConfirm(ctx context.Context, in ConfirmRequest) (string, error) {
	if err := u.otp.CheckConfirm(ctx, in.Phone, in.Code); err != nil {
		return "", err
	}
	token, err := u.tokens.NewResetToken(in.Phone, resetTokenTTL)
	if err != nil {
		return "", httpx.Internal(err)
	}
	return token, nil
}

// ResetSet consumes a reset token and sets the new password.
func (u *Usecase) ResetSet(ctx context.Context, in ResetSetRequest) error {
	phone, err := u.tokens.VerifyResetToken(in.Token)
	if err != nil {
		return httpx.Unauthorized("auth.invalid_reset", "invalid or expired reset token")
	}
	return u.users.ResetPassword(ctx, phone, in.Password)
}

// Refresh validates a refresh token, rotates it (revoking the presented one),
// and issues a fresh pair.
func (u *Usecase) Refresh(ctx context.Context, in RefreshRequest) (TokenResponse, error) {
	uidStr, jti, err := u.tokens.VerifyRefreshToken(in.RefreshToken)
	if err != nil {
		return TokenResponse{}, httpx.Unauthorized("auth.invalid_refresh", "invalid refresh token")
	}
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		return TokenResponse{}, httpx.Unauthorized("auth.invalid_refresh", "invalid refresh token")
	}

	active, err := u.store.IsActive(ctx, jti)
	if err != nil {
		return TokenResponse{}, httpx.Internal(err)
	}
	if !active {
		return TokenResponse{}, httpx.Unauthorized("auth.invalid_refresh", "refresh token revoked or expired")
	}

	usr, err := u.users.GetByID(ctx, uid)
	if err != nil {
		return TokenResponse{}, err
	}
	if err := u.store.Revoke(ctx, jti); err != nil {
		return TokenResponse{}, httpx.Internal(err)
	}
	return u.issueTokens(ctx, usr.ID, usr.Role)
}

// Logout revokes the presented refresh token. Idempotent: an invalid token is
// treated as success so logout never leaks token validity.
func (u *Usecase) Logout(ctx context.Context, in RefreshRequest) error {
	_, jti, err := u.tokens.VerifyRefreshToken(in.RefreshToken)
	if err != nil {
		return nil
	}
	if err := u.store.Revoke(ctx, jti); err != nil {
		return httpx.Internal(err)
	}
	return nil
}

func (u *Usecase) issueTokens(ctx context.Context, userID uuid.UUID, role string) (TokenResponse, error) {
	access, accessExp, err := u.tokens.NewAccessToken(userID, role)
	if err != nil {
		return TokenResponse{}, httpx.Internal(err)
	}
	refresh, jti, refreshExp, err := u.tokens.NewRefreshToken(userID)
	if err != nil {
		return TokenResponse{}, httpx.Internal(err)
	}
	if err := u.store.Save(ctx, jti, userID, refreshExp); err != nil {
		return TokenResponse{}, httpx.Internal(err)
	}
	return TokenResponse{
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
		TokenType:        "Bearer",
	}, nil
}
