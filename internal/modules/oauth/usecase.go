package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/botspace/backend/internal/modules/auth"
	"github.com/google/uuid"
)

// TokenService is the auth-module surface the token endpoint needs: mint a
// fresh pair for a validated user, or rotate a refresh token. auth.Usecase
// satisfies this structurally (same narrow-interface convention as the project
// module's resolvers).
type TokenService interface {
	IssueTokensForUser(ctx context.Context, userID uuid.UUID) (auth.TokenResponse, error)
	Refresh(ctx context.Context, in auth.RefreshRequest) (auth.TokenResponse, error)
}

// Config carries the public URLs advertised in the discovery document plus the
// authorization-code lifetime. Defaults mirror the values the legacy Django
// server advertised so existing MCP clients keep working unchanged.
type Config struct {
	Issuer       string        // e.g. https://api.botspace.uz
	AuthorizeURL string        // frontend consent page, e.g. https://botspace.uz/oauth/authorize
	AccessTTL    time.Duration // used for the token response's expires_in
	CodeTTL      time.Duration // authorization-code lifetime
}

// OAuthError is a spec-shaped token-endpoint error ({error, error_description})
// distinct from the app's generic envelope — MCP clients parse these fields.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oauthErr(code, desc string) *OAuthError { return &OAuthError{Code: code, Description: desc} }

type Usecase struct {
	store  Store
	tokens TokenService
	cfg    Config
}

func NewUsecase(store Store, tokens TokenService, cfg Config) *Usecase {
	if cfg.CodeTTL <= 0 {
		cfg.CodeTTL = 10 * time.Minute
	}
	return &Usecase{store: store, tokens: tokens, cfg: cfg}
}

// Metadata is the RFC 8414 authorization-server discovery document.
func (u *Usecase) Metadata() map[string]any {
	issuer := strings.TrimRight(u.cfg.Issuer, "/")
	return map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                u.cfg.AuthorizeURL,
		"token_endpoint":                        issuer + "/auth/oauth/token/",
		"registration_endpoint":                 issuer + "/auth/oauth/register/",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp"},
	}
}

// RegisterRequest / RegisterResponse mirror the RFC 7591 dynamic client
// registration shapes the MCP client sends/expects.
type RegisterRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type RegisterResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

func (u *Usecase) Register(ctx context.Context, in RegisterRequest) (*RegisterResponse, *OAuthError) {
	if len(in.RedirectURIs) == 0 {
		return nil, oauthErr("invalid_client_metadata", "redirect_uris is required")
	}
	for _, uri := range in.RedirectURIs {
		if _, err := url.ParseRequestURI(uri); err != nil {
			return nil, oauthErr("invalid_redirect_uri", "redirect_uris must be absolute URIs")
		}
	}
	grants := in.GrantTypes
	if len(grants) == 0 {
		grants = []string{"authorization_code", "refresh_token"}
	}
	respTypes := in.ResponseTypes
	if len(respTypes) == 0 {
		respTypes = []string{"code"}
	}
	authMethod := in.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "none"
	}

	c := &Client{
		ClientID:                randToken(24),
		ClientName:              in.ClientName,
		RedirectURIs:            in.RedirectURIs,
		GrantTypes:              grants,
		ResponseTypes:           respTypes,
		TokenEndpointAuthMethod: authMethod,
	}
	if err := u.store.CreateClient(ctx, c); err != nil {
		return nil, oauthErr("server_error", "could not register client")
	}
	return &RegisterResponse{
		ClientID:                c.ClientID,
		ClientIDIssuedAt:        time.Now().Unix(),
		RedirectURIs:            c.RedirectURIs,
		ClientName:              c.ClientName,
		TokenEndpointAuthMethod: c.TokenEndpointAuthMethod,
		GrantTypes:              c.GrantTypes,
		ResponseTypes:           c.ResponseTypes,
	}, nil
}

// ErrUnknownClient / ErrBadRedirect surface as 400s on the consent path.
var (
	ErrUnknownClient = errors.New("unknown client_id")
	ErrBadRedirect   = errors.New("redirect_uri not registered for this client")
)

// AuthorizeMeta validates the client + redirect_uri for the consent screen and
// returns what to show the user.
func (u *Usecase) AuthorizeMeta(ctx context.Context, clientID, redirectURI string) (clientName, redirect string, err error) {
	c, err := u.store.GetClient(ctx, clientID)
	if errors.Is(err, ErrNotFound) {
		return "", "", ErrUnknownClient
	}
	if err != nil {
		return "", "", err
	}
	if !contains(c.RedirectURIs, redirectURI) {
		return "", "", ErrBadRedirect
	}
	return c.ClientName, redirectURI, nil
}

// ApproveParams is the authorization request the authenticated user consents to.
type ApproveParams struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
	Scope               string
	Resource            string
}

// Approve mints a single-use authorization code for the consenting user and
// returns the redirect URL (with code + state) the browser should navigate to.
func (u *Usecase) Approve(ctx context.Context, userID uuid.UUID, p ApproveParams) (string, error) {
	c, err := u.store.GetClient(ctx, p.ClientID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrUnknownClient
	}
	if err != nil {
		return "", err
	}
	if !contains(c.RedirectURIs, p.RedirectURI) {
		return "", ErrBadRedirect
	}
	if p.ResponseType != "code" {
		return "", errors.New("unsupported response_type")
	}
	if p.CodeChallenge == "" || p.CodeChallengeMethod != "S256" {
		return "", errors.New("PKCE S256 code_challenge is required")
	}

	code := randToken(32)
	if err := u.store.SaveCode(ctx, &AuthCode{
		Code:                code,
		ClientID:            p.ClientID,
		UserID:              userID,
		RedirectURI:         p.RedirectURI,
		CodeChallenge:       p.CodeChallenge,
		CodeChallengeMethod: p.CodeChallengeMethod,
		Scope:               p.Scope,
		Resource:            p.Resource,
		ExpiresAt:           time.Now().Add(u.cfg.CodeTTL),
	}); err != nil {
		return "", err
	}
	return buildRedirect(p.RedirectURI, code, p.State), nil
}

// TokenRequest is the (form-encoded) token endpoint input, covering both the
// authorization_code and refresh_token grants.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	CodeVerifier string
	RefreshToken string
}

// TokenResult is the spec-shaped successful token response.
type TokenResult struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

func (u *Usecase) Token(ctx context.Context, in TokenRequest) (*TokenResult, *OAuthError) {
	switch in.GrantType {
	case "authorization_code":
		return u.tokenFromCode(ctx, in)
	case "refresh_token":
		return u.tokenFromRefresh(ctx, in)
	default:
		return nil, oauthErr("unsupported_grant_type", "unsupported grant_type")
	}
}

func (u *Usecase) tokenFromCode(ctx context.Context, in TokenRequest) (*TokenResult, *OAuthError) {
	if in.Code == "" {
		return nil, oauthErr("invalid_request", "code is required")
	}
	ac, err := u.store.TakeCode(ctx, in.Code)
	if errors.Is(err, ErrNotFound) {
		return nil, oauthErr("invalid_grant", "Authorization code is invalid or expired")
	}
	if err != nil {
		return nil, oauthErr("server_error", "could not read authorization code")
	}
	if in.ClientID != "" && in.ClientID != ac.ClientID {
		return nil, oauthErr("invalid_grant", "client_id mismatch")
	}
	if in.RedirectURI != ac.RedirectURI {
		return nil, oauthErr("invalid_grant", "redirect_uri mismatch")
	}
	if !verifyPKCE(in.CodeVerifier, ac.CodeChallenge) {
		return nil, oauthErr("invalid_grant", "PKCE verification failed")
	}

	pair, err := u.tokens.IssueTokensForUser(ctx, ac.UserID)
	if err != nil {
		return nil, oauthErr("server_error", "could not issue token")
	}
	return u.result(pair, ac.Scope), nil
}

func (u *Usecase) tokenFromRefresh(ctx context.Context, in TokenRequest) (*TokenResult, *OAuthError) {
	if in.RefreshToken == "" {
		return nil, oauthErr("invalid_request", "refresh_token is required")
	}
	pair, err := u.tokens.Refresh(ctx, auth.RefreshRequest{RefreshToken: in.RefreshToken})
	if err != nil {
		return nil, oauthErr("invalid_grant", "refresh_token is invalid or expired")
	}
	return u.result(pair, "mcp"), nil
}

func (u *Usecase) result(pair auth.TokenResponse, scope string) *TokenResult {
	expiresIn := int64(u.cfg.AccessTTL / time.Second)
	if expiresIn <= 0 {
		if d := time.Until(pair.AccessExpiresAt); d > 0 {
			expiresIn = int64(d / time.Second)
		}
	}
	return &TokenResult{
		AccessToken:  pair.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    expiresIn,
		RefreshToken: pair.RefreshToken,
		Scope:        scope,
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func randToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// verifyPKCE checks base64url(SHA256(verifier)) == challenge in constant time.
func verifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func buildRedirect(redirectURI, code, state string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		// Fall back to a raw concatenation; redirectURI was validated at
		// registration/approve time, so this branch is effectively unreachable.
		sep := "?"
		if strings.Contains(redirectURI, "?") {
			sep = "&"
		}
		out := redirectURI + sep + "code=" + url.QueryEscape(code)
		if state != "" {
			out += "&state=" + url.QueryEscape(state)
		}
		return out
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
