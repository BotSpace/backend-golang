package credential

import (
	"net/http"
	"net/url"
	"time"
)

// This file ports core/apps/credentials/oauth_providers.py's OAUTH_PROVIDERS
// registry — currently just "google". Django also resolves client_id/secret
// from admin-editable DB config (OptionsModel) with an env var fallback;
// this backend only implements the env var fallback (OAuthConfig, wired from
// APP_GOOGLE_OAUTH_CLIENT_ID/_SECRET in server.go) — porting the DB-backed
// admin override is out of scope for this endpoint.

// oauthProvider is the resolved (non-empty-checked) provider config used to
// build an authorize_url and, for the callback, exchange a code for tokens.
type oauthProvider struct {
	AuthorizeURL string
	TokenURL     string
	UserinfoURL  string
	ClientID     string
	ClientSecret string
	ExtraParams  map[string]string
}

// defaultOAuthCallbackBase is used when OAuthConfig.CallbackBaseURL is empty
// (e.g. a handwritten OAuthConfig in a test, or a config.yaml predating this
// field) — same default as config.go's "google.oauth_callback_base".
const defaultOAuthCallbackBase = "https://api.botspace.uz"

// defaultOAuthPanelURL mirrors Django's OAuthCallbackView _PANEL_URL — used
// when OAuthConfig.PanelURL is empty.
const defaultOAuthPanelURL = "https://admin.botspace.uz"

// OAuthConfig carries the provider app credentials and endpoints the usecase
// needs for both OAuthStart and OAuthCallback. Only Google is ported today —
// CredentialType.Provider ("google") is the lookup key, matching registry.go's
// TypeDef.Provider field.
type OAuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string

	// GoogleTokenURL / GoogleUserinfoURL override Google's real token/userinfo
	// endpoints — empty means use the real Google endpoints. Only meant for
	// tests (point them at an httptest.Server so OAuthCallback's code
	// exchange doesn't hit the network).
	GoogleTokenURL    string
	GoogleUserinfoURL string

	// CallbackBaseURL is the public API origin the redirect_uri is built
	// from — see config.go's GoogleConfig.OAuthCallbackBase doc comment.
	// Empty falls back to defaultOAuthCallbackBase.
	CallbackBaseURL string

	// PanelURL is the dashboard/admin panel origin the callback's HTML
	// response links/postMessages back to. Empty falls back to
	// defaultOAuthPanelURL.
	PanelURL string

	// HTTPClient is used for the callback's token-exchange + userinfo calls.
	// nil builds a client with a 15s timeout (matches Django's
	// requests.post(..., timeout=15)) on first use.
	HTTPClient *http.Client
}

// httpClient returns cfg.HTTPClient if set, else a client with a 15s timeout.
func (cfg OAuthConfig) httpClient() *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// panelURL returns cfg.PanelURL, defaulting to defaultOAuthPanelURL.
func (cfg OAuthConfig) panelURL() string {
	if cfg.PanelURL == "" {
		return defaultOAuthPanelURL
	}
	return cfg.PanelURL
}

// getOAuthProvider mirrors get_provider(name). Returns (nil, false) for an
// unknown provider name — the handler maps that to 404, same as Django.
func getOAuthProvider(name string, cfg OAuthConfig) (*oauthProvider, bool) {
	switch name {
	case "google":
		tokenURL := cfg.GoogleTokenURL
		if tokenURL == "" {
			tokenURL = "https://oauth2.googleapis.com/token"
		}
		userinfoURL := cfg.GoogleUserinfoURL
		if userinfoURL == "" {
			userinfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"
		}
		return &oauthProvider{
			AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     tokenURL,
			UserinfoURL:  userinfoURL,
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			ExtraParams: map[string]string{
				"access_type":            "offline",
				"prompt":                 "consent",
				"include_granted_scopes": "true",
			},
		}, true
	default:
		return nil, false
	}
}

// oauthCallbackURL mirrors apps/credentials/services/oauth.py's CALLBACK_URL
// constant, now pointed at this backend instead of Django. base is
// OAuthConfig.CallbackBaseURL (empty -> defaultOAuthCallbackBase). The path
// is fixed to /api/v2/... (not /api/v1) so a browser hitting it goes through
// the prod gateway's public /api/v2 -> internal /api/v1 rewrite and reaches
// this backend's /api/v1/credentials/oauth/{provider}/callback route (see
// routes.go). This exact string is registered in Google Console as the
// redirect_uri and MUST be resent byte-for-byte by the callback's token
// exchange call.
func oauthCallbackURL(base, provider string) string {
	if base == "" {
		base = defaultOAuthCallbackBase
	}
	return base + "/api/v2/credentials/oauth/" + url.PathEscape(provider) + "/callback/"
}
