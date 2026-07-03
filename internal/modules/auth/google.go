package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// googleTokenInfoURL is Google's token-verification endpoint. It validates the
// ID token's signature/expiry/issuer server-side and echoes back the claims —
// no local JWKS handling or extra SDK dependency needed. Google documents this
// as suitable for server-side, moderate-volume verification (see
// https://developers.google.com/identity/sign-in/web/backend-auth); a
// higher-volume deployment could switch to local JWKS verification
// (google.golang.org/api/idtoken) without changing the GoogleVerifier
// interface below.
const googleTokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"

// GoogleClaims is the subset of a verified Google ID token this module needs.
type GoogleClaims struct {
	Email         string
	EmailVerified bool
	FirstName     string
	LastName      string
	Sub           string // Google's stable per-account subject id
}

// GoogleVerifier validates a Google ID token (the `credential`/`id_token` a
// Google Identity Services button hands the frontend) and returns its claims.
// An interface so tests can fake it without hitting the network.
type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (GoogleClaims, error)
}

// tokenInfoVerifier calls Google's tokeninfo endpoint and checks the audience
// against our configured OAuth client ID (APP_GOOGLE_CLIENT_ID) — without
// that check anyone could hand us a validly-signed ID token issued to a
// *different* app and we'd log them in as that email.
type tokenInfoVerifier struct {
	audience string
	client   *http.Client
}

// NewGoogleVerifier builds the default GoogleVerifier. audience is the
// expected `aud` claim — the dashboard's Google OAuth client ID
// (APP_GOOGLE_CLIENT_ID, same value baked into the frontend as
// NEXT_PUBLIC_GOOGLE_CLIENT_ID).
func NewGoogleVerifier(audience string) GoogleVerifier {
	return &tokenInfoVerifier{
		audience: audience,
		client:   &http.Client{Timeout: 8 * time.Second},
	}
}

// tokenInfoResponse mirrors the fields tokeninfo returns for a valid Google
// ID token. All fields arrive as JSON strings (including booleans), per
// Google's documented response shape.
type tokenInfoResponse struct {
	Aud           string `json:"aud"`
	Email         string `json:"email"`
	EmailVerified string `json:"email_verified"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Name          string `json:"name"`
	Sub           string `json:"sub"`
	ErrorDesc     string `json:"error_description"`
}

func (v *tokenInfoVerifier) Verify(ctx context.Context, idToken string) (GoogleClaims, error) {
	if idToken == "" {
		return GoogleClaims{}, fmt.Errorf("empty id_token")
	}
	if v.audience == "" {
		return GoogleClaims{}, fmt.Errorf("google sign-in not configured (APP_GOOGLE_CLIENT_ID unset)")
	}

	q := url.Values{"id_token": {idToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleTokenInfoURL+"?"+q.Encode(), nil)
	if err != nil {
		return GoogleClaims{}, fmt.Errorf("build tokeninfo request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return GoogleClaims{}, fmt.Errorf("call google tokeninfo: %w", err)
	}
	defer resp.Body.Close()

	var body tokenInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return GoogleClaims{}, fmt.Errorf("decode tokeninfo response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		desc := body.ErrorDesc
		if desc == "" {
			desc = fmt.Sprintf("tokeninfo returned status %d", resp.StatusCode)
		}
		return GoogleClaims{}, fmt.Errorf("invalid google id_token: %s", desc)
	}
	if body.Aud != v.audience {
		return GoogleClaims{}, fmt.Errorf("id_token audience mismatch")
	}
	if body.Email == "" {
		return GoogleClaims{}, fmt.Errorf("id_token has no email claim")
	}

	return GoogleClaims{
		Email:         body.Email,
		EmailVerified: body.EmailVerified == "true",
		FirstName:     firstNonEmpty(body.GivenName, body.Name),
		LastName:      body.FamilyName,
		Sub:           body.Sub,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
