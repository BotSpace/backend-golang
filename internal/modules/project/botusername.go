package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// BotUsernameResolver resolves a Telegram bot token to its @username via
// getMe. Implementations are expected to cache results — ResolveUsername is
// called on every project Get(), and a bare getMe would otherwise add a
// Telegram round trip to every single-project fetch. Mirrors Django's
// _resolve_test_bot_username() cache pattern (core/apps/api/views/
// project.py:88-105), generalized here to any token: both the shared
// platform TEST_BOT_TOKEN and a project's own RELEASE_BOT_TOKEN flow through
// the same resolver/cache.
type BotUsernameResolver interface {
	ResolveUsername(ctx context.Context, token string) (string, error)
}

// botUsernameTTL mirrors Django's cache.set(test_bot_username, ..., 3600): an
// hour is long enough that a bot's username (which practically never
// changes) isn't re-resolved on every request, short enough that a token
// rotation or username change is picked up without a redeploy.
const botUsernameTTL = time.Hour

type cachedUsername struct {
	username string
	expires  time.Time
}

// telegramUsernameResolver is the real BotUsernameResolver: getMe over stdlib
// net/http (same convention as authbot.httpBotAPI / project.setReleaseWebhook
// — no third-party Telegram SDK anywhere in this backend), with an in-memory
// per-token TTL cache.
type telegramUsernameResolver struct {
	httpClient *http.Client

	mu    sync.Mutex
	cache map[string]cachedUsername
}

// NewTelegramUsernameResolver builds the real getMe-backed resolver.
func NewTelegramUsernameResolver() BotUsernameResolver {
	return &telegramUsernameResolver{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		cache:      make(map[string]cachedUsername),
	}
}

type getMeResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	Result      struct {
		Username string `json:"username"`
	} `json:"result"`
}

func (r *telegramUsernameResolver) ResolveUsername(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", fmt.Errorf("project: empty bot token")
	}

	r.mu.Lock()
	cached, ok := r.cache[token]
	r.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.username, nil
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("project: build getMe request: %w", err)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("project: getMe call: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("project: read getMe response: %w", err)
	}
	var parsed getMeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("project: decode getMe response (status %d): %w", resp.StatusCode, err)
	}
	if !parsed.OK || parsed.Result.Username == "" {
		return "", fmt.Errorf("project: getMe rejected (status %d): %s", resp.StatusCode, parsed.Description)
	}

	r.mu.Lock()
	r.cache[token] = cachedUsername{username: parsed.Result.Username, expires: time.Now().Add(botUsernameTTL)}
	r.mu.Unlock()

	return parsed.Result.Username, nil
}
