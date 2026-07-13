package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"
	"time"

	"github.com/botspace/backend/internal/modules/auth"
	"github.com/google/uuid"
)

// fakeStore is an in-memory Store for exercising the OAuth flow without Postgres.
type fakeStore struct {
	clients map[string]*Client
	codes   map[string]*AuthCode
}

func newFakeStore() *fakeStore {
	return &fakeStore{clients: map[string]*Client{}, codes: map[string]*AuthCode{}}
}

func (f *fakeStore) CreateClient(_ context.Context, c *Client) error {
	f.clients[c.ClientID] = c
	return nil
}

func (f *fakeStore) GetClient(_ context.Context, clientID string) (*Client, error) {
	c, ok := f.clients[clientID]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (f *fakeStore) SaveCode(_ context.Context, a *AuthCode) error {
	f.codes[a.Code] = a
	return nil
}

func (f *fakeStore) TakeCode(_ context.Context, code string) (*AuthCode, error) {
	a, ok := f.codes[code]
	if !ok {
		return nil, ErrNotFound
	}
	delete(f.codes, code) // single-use
	if time.Now().After(a.ExpiresAt) {
		return nil, ErrNotFound
	}
	return a, nil
}

// fakeTokens records the user it was asked to mint tokens for and returns a
// deterministic pair.
type fakeTokens struct {
	lastUser  uuid.UUID
	refreshOK bool
}

func (f *fakeTokens) IssueTokensForUser(_ context.Context, userID uuid.UUID) (auth.TokenResponse, error) {
	f.lastUser = userID
	return auth.TokenResponse{
		AccessToken:     "access-for-" + userID.String(),
		RefreshToken:    "refresh-for-" + userID.String(),
		AccessExpiresAt: time.Now().Add(15 * time.Minute),
	}, nil
}

func (f *fakeTokens) Refresh(_ context.Context, in auth.RefreshRequest) (auth.TokenResponse, error) {
	f.refreshOK = true
	return auth.TokenResponse{AccessToken: "rotated-access", RefreshToken: "rotated-refresh"}, nil
}

func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newTestUC() (*Usecase, *fakeStore, *fakeTokens) {
	store := newFakeStore()
	tokens := &fakeTokens{}
	uc := NewUsecase(store, tokens, Config{
		Issuer:       "https://api.example.test",
		AuthorizeURL: "https://app.example.test/oauth/authorize",
		AccessTTL:    15 * time.Minute,
		CodeTTL:      10 * time.Minute,
	})
	return uc, store, tokens
}

// TestFullFlow_RegisterApproveToken is the happy path: register a client,
// approve as a user, then redeem the code (with the matching PKCE verifier) for
// tokens minted for that user.
func TestFullFlow_RegisterApproveToken(t *testing.T) {
	uc, _, tokens := newTestUC()
	ctx := context.Background()
	redirect := "http://localhost:6274/oauth/callback"
	verifier := "a-sufficiently-long-random-code-verifier-string-123456"

	reg, oerr := uc.Register(ctx, RegisterRequest{
		ClientName:   "Inspector",
		RedirectURIs: []string{redirect},
	})
	if oerr != nil {
		t.Fatalf("Register: %v", oerr)
	}
	if reg.ClientID == "" {
		t.Fatal("empty client_id")
	}

	user := uuid.New()
	redirectURL, err := uc.Approve(ctx, user, ApproveParams{
		ClientID:            reg.ClientID,
		RedirectURI:         redirect,
		ResponseType:        "code",
		CodeChallenge:       pkce(verifier),
		CodeChallengeMethod: "S256",
		State:               "xyz",
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	u, _ := url.Parse(redirectURL)
	if u.Query().Get("state") != "xyz" {
		t.Fatalf("state not echoed: %q", redirectURL)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}

	res, oerr := uc.Token(ctx, TokenRequest{
		GrantType:    "authorization_code",
		Code:         code,
		RedirectURI:  redirect,
		ClientID:     reg.ClientID,
		CodeVerifier: verifier,
	})
	if oerr != nil {
		t.Fatalf("Token: %v", oerr)
	}
	if res.AccessToken != "access-for-"+user.String() {
		t.Fatalf("access token minted for wrong user: %q", res.AccessToken)
	}
	if res.TokenType != "Bearer" || res.ExpiresIn <= 0 {
		t.Fatalf("bad token response: %+v", res)
	}
	if tokens.lastUser != user {
		t.Fatalf("IssueTokensForUser got %v, want %v", tokens.lastUser, user)
	}
}

func TestToken_WrongPKCEVerifierRejected(t *testing.T) {
	uc, _, _ := newTestUC()
	ctx := context.Background()
	redirect := "http://localhost/cb"
	reg, _ := uc.Register(ctx, RegisterRequest{RedirectURIs: []string{redirect}})
	redirectURL, _ := uc.Approve(ctx, uuid.New(), ApproveParams{
		ClientID: reg.ClientID, RedirectURI: redirect, ResponseType: "code",
		CodeChallenge: pkce("the-real-verifier"), CodeChallengeMethod: "S256",
	})
	u, _ := url.Parse(redirectURL)

	_, oerr := uc.Token(ctx, TokenRequest{
		GrantType: "authorization_code", Code: u.Query().Get("code"),
		RedirectURI: redirect, ClientID: reg.ClientID, CodeVerifier: "the-WRONG-verifier",
	})
	if oerr == nil || oerr.Code != "invalid_grant" {
		t.Fatalf("expected invalid_grant, got %v", oerr)
	}
}

func TestToken_CodeIsSingleUse(t *testing.T) {
	uc, _, _ := newTestUC()
	ctx := context.Background()
	redirect := "http://localhost/cb"
	verifier := "verifier-single-use-xxxxxxxxxxxxxxxxxxxxxxxx"
	reg, _ := uc.Register(ctx, RegisterRequest{RedirectURIs: []string{redirect}})
	redirectURL, _ := uc.Approve(ctx, uuid.New(), ApproveParams{
		ClientID: reg.ClientID, RedirectURI: redirect, ResponseType: "code",
		CodeChallenge: pkce(verifier), CodeChallengeMethod: "S256",
	})
	code := mustCode(t, redirectURL)
	req := TokenRequest{GrantType: "authorization_code", Code: code, RedirectURI: redirect, ClientID: reg.ClientID, CodeVerifier: verifier}

	if _, oerr := uc.Token(ctx, req); oerr != nil {
		t.Fatalf("first redemption failed: %v", oerr)
	}
	if _, oerr := uc.Token(ctx, req); oerr == nil {
		t.Fatal("second redemption of the same code must fail")
	}
}

func TestApprove_UnregisteredRedirectRejected(t *testing.T) {
	uc, _, _ := newTestUC()
	ctx := context.Background()
	reg, _ := uc.Register(ctx, RegisterRequest{RedirectURIs: []string{"http://localhost/cb"}})
	_, err := uc.Approve(ctx, uuid.New(), ApproveParams{
		ClientID: reg.ClientID, RedirectURI: "http://evil.test/cb", ResponseType: "code",
		CodeChallenge: pkce("v"), CodeChallengeMethod: "S256",
	})
	if err != ErrBadRedirect {
		t.Fatalf("expected ErrBadRedirect, got %v", err)
	}
}

func TestAuthorizeMeta_UnknownClient(t *testing.T) {
	uc, _, _ := newTestUC()
	_, _, err := uc.AuthorizeMeta(context.Background(), "nope", "http://localhost/cb")
	if err != ErrUnknownClient {
		t.Fatalf("expected ErrUnknownClient, got %v", err)
	}
}

func mustCode(t *testing.T, redirectURL string) string {
	t.Helper()
	u, err := url.Parse(redirectURL)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return u.Query().Get("code")
}
