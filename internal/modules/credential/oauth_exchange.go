package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This file ports core/apps/credentials/services/oauth.py's exchange_code /
// fetch_userinfo / build_token_data — stdlib net/http only, no new
// dependency, mirroring Django's `requests` calls against the same Google
// endpoints (see oauth_providers.go's getOAuthProvider).

// exchangeOAuthCode POSTs the authorization code to prov.TokenURL and
// returns the raw JSON token response as a map — mirrors exchange_code().
// redirectURI MUST be byte-identical to the one sent in OAuthStart's
// authorize_url (Google rejects the exchange otherwise).
func exchangeOAuthCode(ctx context.Context, client *http.Client, prov *oauthProvider, redirectURI, code string) (map[string]any, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", prov.ClientID)
	form.Set("client_secret", prov.ClientSecret)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return doOAuthJSONRequest(client, req)
}

// fetchOAuthUserinfo GETs prov.UserinfoURL with the access token — mirrors
// fetch_userinfo(). Errors are the caller's decision to ignore (Django
// treats this as best-effort too).
func fetchOAuthUserinfo(ctx context.Context, client *http.Client, prov *oauthProvider, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, prov.UserinfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	return doOAuthJSONRequest(client, req)
}

func doOAuthJSONRequest(client *http.Client, req *http.Request) (map[string]any, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", req.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %d: %s", req.URL, resp.StatusCode, truncate(string(body), 500))
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode json response: %w", err)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// buildOAuthTokenData normalises a raw token response (+ optional userinfo)
// into the data dict stored on the Credential — mirrors build_token_data().
// Field names/shape match Django's byte-for-byte (access_token,
// refresh_token, expires_at, scope, token_type, account_email) so any code
// (engine, admin) reading an oauth2 credential's data sees the same shape
// regardless of which backend created it.
func buildOAuthTokenData(tokenResp, userinfo map[string]any) map[string]any {
	expiresIn := 3600.0
	if v, ok := tokenResp["expires_in"].(float64); ok {
		expiresIn = v
	}
	expiresAt := time.Now().UTC().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)

	data := map[string]any{
		"access_token":  strField(tokenResp, "access_token"),
		"refresh_token": strField(tokenResp, "refresh_token"),
		"expires_at":    expiresAt,
		"scope":         strField(tokenResp, "scope"),
		"token_type":    strFieldOr(tokenResp, "token_type", "Bearer"),
		"account_email": "",
	}
	if userinfo != nil {
		data["account_email"] = strField(userinfo, "email")
	}
	return data
}

func strField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func strFieldOr(m map[string]any, key, fallback string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return fallback
}
