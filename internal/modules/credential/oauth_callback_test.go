package credential

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"testing"

	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/google/uuid"
)

// newCallbackTestUsecase wires a usecase with fake token/userinfo HTTP
// endpoints (httptest servers) instead of the real Google endpoints, so
// OAuthCallback's token exchange never touches the network. tokenHandler
// and userinfoHandler are nil-able; a nil handler leaves that mux route
// unregistered (404), exercising the best-effort userinfo fetch path.
func newCallbackTestUsecase(t *testing.T, repo *fakeRepo, cipher *crypto.Cipher, tokenHandler, userinfoHandler http.HandlerFunc) (Usecase, func()) {
	t.Helper()
	mux := http.NewServeMux()
	if tokenHandler != nil {
		mux.HandleFunc("/token", tokenHandler)
	}
	if userinfoHandler != nil {
		mux.HandleFunc("/userinfo", userinfoHandler)
	}
	srv := httptest.NewServer(mux)

	uc := NewUsecase(repo, cipher, OAuthConfig{
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		GoogleTokenURL:     srv.URL + "/token",
		GoogleUserinfoURL:  srv.URL + "/userinfo",
		CallbackBaseURL:    "https://api.botspace.uz",
	})
	return uc, srv.Close
}

func jsonHandler(t *testing.T, status int, body map[string]any) http.HandlerFunc {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal fake response: %v", err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	}
}

func stateFromAuthorizeURL(t *testing.T, authorizeURL string) string {
	t.Helper()
	parsed, err := neturl.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url %q: %v", authorizeURL, err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("authorize_url %q has no state param", authorizeURL)
	}
	return state
}

// TestOAuthCallback_HappyPath drives the full flow: mint a state via
// OAuthStart (the real signing path), then complete it via OAuthCallback
// against a fake token+userinfo server, and asserts a credential was
// created for the state's owner with the expected token data shape,
// reachable through the normal encrypt/decrypt Get path.
func TestOAuthCallback_HappyPath(t *testing.T) {
	t.Setenv("DJANGO_SECRET_KEY", "oauth-callback-happy-secret")

	tokenHandler := jsonHandler(t, 200, map[string]any{
		"access_token":  "fake-access-token",
		"refresh_token": "fake-refresh-token",
		"expires_in":    3600,
		"scope":         "https://www.googleapis.com/auth/spreadsheets",
		"token_type":    "Bearer",
	})
	userinfoHandler := jsonHandler(t, 200, map[string]any{"email": "owner@example.com"})

	repo, cipher := newFakeRepo(), mustCipher(t)
	uc, closeSrv := newCallbackTestUsecase(t, repo, cipher, tokenHandler, userinfoHandler)
	defer closeSrv()

	ownerID := uuid.New()
	ctx := context.Background()

	authorizeURL, err := uc.OAuthStart(ctx, ownerID, "google", "google_sheets")
	if err != nil {
		t.Fatalf("OAuthStart: %v", err)
	}
	state := stateFromAuthorizeURL(t, authorizeURL)

	if err := uc.OAuthCallback(ctx, "google", "fake-code", state, ""); err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}

	var found *Credential
	for _, c := range repo.byID {
		if c.OwnerID == ownerID {
			cp := *c
			found = &cp
			break
		}
	}
	if found == nil {
		t.Fatal("OAuthCallback did not create a credential for the state's owner")
	}
	if found.TypeKey != "google_sheets" {
		t.Fatalf("credential type_key = %q, want google_sheets", found.TypeKey)
	}
	if want := "Google Sheets (owner@example.com)"; found.Name != want {
		t.Fatalf("credential name = %q, want %q", found.Name, want)
	}

	detail, err := uc.Get(ctx, found.ID, ownerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.Data["access_token"] != "fake-access-token" {
		t.Fatalf("access_token = %v, want fake-access-token", detail.Data["access_token"])
	}
	if detail.Data["refresh_token"] != "fake-refresh-token" {
		t.Fatalf("refresh_token = %v, want fake-refresh-token", detail.Data["refresh_token"])
	}
	if detail.Data["account_email"] != "owner@example.com" {
		t.Fatalf("account_email = %v, want owner@example.com", detail.Data["account_email"])
	}
	if _, ok := detail.Data["expires_at"].(string); !ok {
		t.Fatalf("expires_at missing or not a string: %v", detail.Data["expires_at"])
	}

	// Owner-facing projection must mask the tokens like any other secret.
	resp := toResponse(detail)
	if resp.Data["access_token"] == "fake-access-token" {
		t.Fatal("owner-facing response must mask access_token, got plaintext")
	}
}

// TestOAuthCallback_Reconnect exercises the upsert path: a second
// OAuthCallback for the same (owner, type_key, account_email) refreshes the
// existing credential's tokens instead of creating a duplicate — mirrors
// Django's _upsert_credential re-connect behaviour. Both callbacks share the
// same repo/cipher (only the fake token server is swapped between them, to
// prove the second exchange's tokens are what land in the row).
func TestOAuthCallback_Reconnect(t *testing.T) {
	t.Setenv("DJANGO_SECRET_KEY", "oauth-callback-reconnect-secret")
	repo, cipher := newFakeRepo(), mustCipher(t)
	userinfoHandler := jsonHandler(t, 200, map[string]any{"email": "reconnect@example.com"})
	ownerID := uuid.New()
	ctx := context.Background()

	firstToken := jsonHandler(t, 200, map[string]any{"access_token": "token-v1", "refresh_token": "refresh-v1", "expires_in": 3600})
	uc1, close1 := newCallbackTestUsecase(t, repo, cipher, firstToken, userinfoHandler)
	authorizeURL1, err := uc1.OAuthStart(ctx, ownerID, "google", "google_drive")
	if err != nil {
		t.Fatalf("first OAuthStart: %v", err)
	}
	if err := uc1.OAuthCallback(ctx, "google", "code-1", stateFromAuthorizeURL(t, authorizeURL1), ""); err != nil {
		t.Fatalf("first OAuthCallback: %v", err)
	}
	close1()
	if got := len(repo.byID); got != 1 {
		t.Fatalf("expected 1 credential after first callback, got %d", got)
	}

	secondToken := jsonHandler(t, 200, map[string]any{"access_token": "token-v2", "refresh_token": "refresh-v2", "expires_in": 3600})
	uc2, close2 := newCallbackTestUsecase(t, repo, cipher, secondToken, userinfoHandler)
	defer close2()
	authorizeURL2, err := uc2.OAuthStart(ctx, ownerID, "google", "google_drive")
	if err != nil {
		t.Fatalf("second OAuthStart: %v", err)
	}
	if err := uc2.OAuthCallback(ctx, "google", "code-2", stateFromAuthorizeURL(t, authorizeURL2), ""); err != nil {
		t.Fatalf("second OAuthCallback: %v", err)
	}

	if got := len(repo.byID); got != 1 {
		t.Fatalf("expected reconnect to reuse the existing credential (1 row), got %d", got)
	}
	var refreshed *Credential
	for _, c := range repo.byID {
		refreshed = c
	}
	detail, err := uc2.Get(ctx, refreshed.ID, ownerID)
	if err != nil {
		t.Fatalf("Get after reconnect: %v", err)
	}
	if detail.Data["access_token"] != "token-v2" {
		t.Fatalf("access_token after reconnect = %v, want token-v2 (refreshed, not duplicated)", detail.Data["access_token"])
	}
}

// TestOAuthCallback_ProviderError mirrors Google redirecting back with
// ?error=access_denied (user declined consent) — no code/state exchange
// should be attempted, and a *httpx.AppError with 400 status must come back
// so the handler renders the error HTML page instead of a JSON 500.
func TestOAuthCallback_ProviderError(t *testing.T) {
	uc, closeSrv := newCallbackTestUsecase(t, newFakeRepo(), mustCipher(t), nil, nil)
	defer closeSrv()

	err := uc.OAuthCallback(context.Background(), "google", "", "", "access_denied")
	if err == nil {
		t.Fatal("expected an error for a provider error param")
	}
	if got := status(err); got != 400 {
		t.Fatalf("expected 400 for provider error, got %d (%v)", got, err)
	}
}

// TestOAuthCallback_MissingCodeOrState covers the other AllowAny-path guard
// Django's OAuthCallbackView has: no error param, but code/state absent too
// (e.g. someone hits the callback URL directly).
func TestOAuthCallback_MissingCodeOrState(t *testing.T) {
	uc, closeSrv := newCallbackTestUsecase(t, newFakeRepo(), mustCipher(t), nil, nil)
	defer closeSrv()

	if err := uc.OAuthCallback(context.Background(), "google", "", "", ""); status(err) != 400 {
		t.Fatalf("expected 400 for missing code/state, got %v", err)
	}
}

// TestOAuthCallback_BadState ensures a tampered/garbage state token is
// rejected before any token exchange is attempted.
func TestOAuthCallback_BadState(t *testing.T) {
	t.Setenv("DJANGO_SECRET_KEY", "oauth-callback-badstate-secret")
	uc, closeSrv := newCallbackTestUsecase(t, newFakeRepo(), mustCipher(t), nil, nil)
	defer closeSrv()

	err := uc.OAuthCallback(context.Background(), "google", "some-code", "not-a-valid-state-token", "")
	if status(err) != 400 {
		t.Fatalf("expected 400 for bad state, got %v", err)
	}
}

// TestOAuthCallback_TokenExchangeFailure ensures a non-2xx from the token
// endpoint surfaces as a 500 *httpx.AppError (the handler maps this to a
// generic "something went wrong" HTML message, never leaking the raw
// provider response — see handler.go's oauthErrorMessage).
func TestOAuthCallback_TokenExchangeFailure(t *testing.T) {
	t.Setenv("DJANGO_SECRET_KEY", "oauth-callback-exchfail-secret")
	failHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}
	uc, closeSrv := newCallbackTestUsecase(t, newFakeRepo(), mustCipher(t), failHandler, nil)
	defer closeSrv()

	ownerID := uuid.New()
	ctx := context.Background()
	authorizeURL, err := uc.OAuthStart(ctx, ownerID, "google", "google_email")
	if err != nil {
		t.Fatalf("OAuthStart: %v", err)
	}
	state := stateFromAuthorizeURL(t, authorizeURL)

	err = uc.OAuthCallback(ctx, "google", "bad-code", state, "")
	if status(err) != 500 {
		t.Fatalf("expected 500 for token exchange failure, got %v", err)
	}
}

func mustCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New([]byte("test-key-for-credential-oauth-callback"))
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return c
}
