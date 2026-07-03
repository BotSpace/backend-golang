package credential

import "net/url"

// This file ports core/apps/credentials/oauth_providers.py's OAUTH_PROVIDERS
// registry — currently just "google". Django also resolves client_id/secret
// from admin-editable DB config (OptionsModel) with an env var fallback;
// this backend only implements the env var fallback (OAuthConfig, wired from
// APP_GOOGLE_OAUTH_CLIENT_ID/_SECRET in server.go) — porting the DB-backed
// admin override is out of scope for this endpoint.

// oauthProvider is the resolved (non-empty-checked) provider config used to
// build an authorize_url.
type oauthProvider struct {
	AuthorizeURL string
	ClientID     string
	ExtraParams  map[string]string
}

// OAuthConfig carries the provider app credentials the usecase needs. Only
// Google is ported today — CredentialType.Provider ("google") is the lookup
// key, matching registry.go's TypeDef.Provider field.
type OAuthConfig struct {
	GoogleClientID     string
	GoogleClientSecret string
}

// getOAuthProvider mirrors get_provider(name). Returns (nil, false) for an
// unknown provider name — the handler maps that to 404, same as Django.
func getOAuthProvider(name string, cfg OAuthConfig) (*oauthProvider, bool) {
	switch name {
	case "google":
		return &oauthProvider{
			AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
			ClientID:     cfg.GoogleClientID,
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
// constant — a hardcoded absolute Django URL (NOT under /api/v2), since the
// callback itself stays on Django (see oauth_state.go's package comment).
func oauthCallbackURL(provider string) string {
	return "https://api.botspace.uz/api/v1/credentials/oauth/" + url.PathEscape(provider) + "/callback/"
}
