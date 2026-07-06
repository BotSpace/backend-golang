package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// claimTTL mirrors Django's cache.set(..., 900) — a wizard-side username
// claim is only honoured for 15 minutes.
const claimTTL = 15 * time.Minute

// telegramTimeout bounds the manager-bot API call, mirroring Django's
// httpx.get(timeout=10).
const telegramTimeout = 10 * time.Second

// ManagedBotConfig is the wizard-facing deep-link configuration. Enabled is
// false (and the other fields empty) when the manager bot is not configured.
type ManagedBotConfig struct {
	Enabled          bool
	ManagerUsername  string
	DeepLinkTemplate string
}

// ManagedBotWebhookPayload is the Telegram "managed_bot" update the manager
// bot forwards on bot creation. Bot.ID == 0 means no managed_bot payload was
// present — the caller should ack (200) and do nothing, same as Django.
type ManagedBotWebhookPayload struct {
	Bot struct {
		ID        int64
		Username  string
		FirstName string
	}
	CreatorID int64
}

// Usecase exposes the webhook-endpoint and managed-bot operations.
type Usecase interface {
	// MintEndpoint is the owner-scoped get-or-create for a node's webhook URL.
	MintEndpoint(ctx context.Context, in MintInput) (*WebhookEndpoint, error)
	// ResolveToken is the internal lookup used by the webhook-hook forwarder.
	// internalToken must equal APP_INTERNAL_TOKEN or the call is rejected.
	ResolveToken(ctx context.Context, internalToken string, token uuid.UUID) (*ResolvedHook, error)

	ListManagedBots(ctx context.Context, ownerID uuid.UUID) ([]ManagedBot, error)
	ManagedBotConfig() ManagedBotConfig
	// ResolveManagedBotToken loads a manager-bot-wizard-created bot
	// owner-scoped by id, decrypts its stored token, marks it used, and
	// returns the plaintext — mirrors Django's ProjectView._attach_managed_bot
	// / create_from_template's managed_bot_id branch (ManagedBotModel.objects.
	// get(id=managed_bot_id, owner=request.user) + get_token() +
	// managed_bot.used = True). Consumed by the project module (see
	// project.ManagedBotTokenResolver) so a project can attach a managed
	// bot's token as its own RELEASE_BOT_TOKEN without this package needing
	// to import the project module.
	ResolveManagedBotToken(ctx context.Context, managedBotID, ownerID uuid.UUID) (string, error)
	// ClaimManagedBotUsername reserves a not-yet-created bot's username for
	// ownerID for claimTTL — consumed by HandleManagedBotWebhook once
	// Telegram confirms the bot's creation.
	ClaimManagedBotUsername(ownerID uuid.UUID, username string) error
	// HandleManagedBotWebhook processes a Telegram "managed_bot" update:
	// fetches the bot token from the manager bot API, encrypts it, and
	// upserts the managed_bot row. secret/headerSecret must both equal
	// MANAGER_BOT_WEBHOOK_SECRET.
	HandleManagedBotWebhook(ctx context.Context, secret, headerSecret string, payload ManagedBotWebhookPayload) error
}

type claimEntry struct {
	ownerID   uuid.UUID
	expiresAt time.Time
}

type usecase struct {
	repo       Repository
	cipher     *crypto.Cipher
	httpClient *http.Client
	// fetchToken defaults to fetchManagedBotToken (a real Telegram API call)
	// but is a field so tests can substitute a fake without touching the
	// network — see usecase_test.go.
	fetchToken func(ctx context.Context, managerToken string, botUserID int64) (string, error)

	// mu/claims implement the wizard's "pre-claim a username" flow in
	// process memory (Django uses a shared Redis cache). This is
	// single-instance only and lost on restart — acceptable for a 15-minute
	// wizard claim, but swap for a shared store if the API ever runs with
	// multiple replicas (same caveat as middleware.RateLimit).
	mu     sync.Mutex
	claims map[string]claimEntry
}

// NewUsecase wires the webhook usecase. cipher encrypts managed-bot tokens
// at rest.
func NewUsecase(repo Repository, cipher *crypto.Cipher) Usecase {
	u := &usecase{
		repo:       repo,
		cipher:     cipher,
		httpClient: &http.Client{Timeout: telegramTimeout},
		claims:     make(map[string]claimEntry),
	}
	u.fetchToken = u.fetchManagedBotToken
	return u
}

func (u *usecase) MintEndpoint(ctx context.Context, in MintInput) (*WebhookEndpoint, error) {
	in.NodeID = strings.TrimSpace(in.NodeID)
	if in.NodeID == "" {
		return nil, httpx.BadRequest("webhook.node_id_required", "node_id majburiy")
	}
	e, err := u.repo.MintEndpoint(ctx, in)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// Covers both "project does not exist" and "not owned by the
			// caller" — the single EXISTS-gated SQL statement cannot tell
			// them apart, and PORTING_GUIDE rule 4 maps both to 404 anyway.
			return nil, httpx.NotFound("webhook.project_not_found", "project topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return e, nil
}

func (u *usecase) ResolveToken(ctx context.Context, internalToken string, token uuid.UUID) (*ResolvedHook, error) {
	expected := os.Getenv("APP_INTERNAL_TOKEN")
	if expected == "" || internalToken != expected {
		return nil, httpx.Unauthorized("webhook.invalid_internal_token", "unauthorized")
	}
	hook, err := u.repo.ResolveToken(ctx, token)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("webhook.token_not_found", "webhook not found")
		}
		return nil, httpx.Internal(err)
	}
	return hook, nil
}

func (u *usecase) ListManagedBots(ctx context.Context, ownerID uuid.UUID) ([]ManagedBot, error) {
	bots, err := u.repo.ListManagedBotsForOwner(ctx, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return bots, nil
}

func (u *usecase) ResolveManagedBotToken(ctx context.Context, managedBotID, ownerID uuid.UUID) (string, error) {
	bot, err := u.repo.GetManagedBotByIDForOwner(ctx, managedBotID, ownerID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return "", httpx.NotFound("managed_bot.not_found", "managed bot topilmadi")
		}
		return "", httpx.Internal(err)
	}
	if u.cipher == nil {
		return "", httpx.Internal(errors.New("webhook: crypto cipher not configured"))
	}
	plain, err := u.cipher.Decrypt(bot.TokenEncrypted)
	if err != nil {
		return "", httpx.Internal(fmt.Errorf("decrypt managed bot token: %w", err))
	}
	// Best-effort, same convention as HandleManagedBotWebhook's own upsert —
	// a failed "used" flip must not fail an otherwise-successful token
	// resolution (the bot is already about to be attached to the project).
	if err := u.repo.MarkManagedBotUsed(ctx, bot.ID); err != nil {
		zap.L().Warn("webhook mark managed bot used failed",
			logger.String("managed_bot_id", bot.ID.String()),
			logger.Err(err),
		)
	}
	return string(plain), nil
}

func (u *usecase) ManagedBotConfig() ManagedBotConfig {
	username := os.Getenv("MANAGER_BOT_USERNAME")
	token := os.Getenv("MANAGER_BOT_TOKEN")
	if username == "" || token == "" {
		return ManagedBotConfig{}
	}
	return ManagedBotConfig{
		Enabled:          true,
		ManagerUsername:  username,
		DeepLinkTemplate: fmt.Sprintf("https://t.me/newbot/%s/{username}?name={name}", username),
	}
}

func (u *usecase) ClaimManagedBotUsername(ownerID uuid.UUID, username string) error {
	username = normalizeUsername(username)
	if username == "" {
		return httpx.BadRequest("managed_bot.username_required", "username majburiy")
	}
	u.mu.Lock()
	u.claims[username] = claimEntry{ownerID: ownerID, expiresAt: time.Now().Add(claimTTL)}
	u.mu.Unlock()
	return nil
}

// resolveClaim consumes (deletes) any pending claim for username, returning
// its owner if the claim is still within claimTTL.
func (u *usecase) resolveClaim(username string) *uuid.UUID {
	username = normalizeUsername(username)
	if username == "" {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	entry, ok := u.claims[username]
	if !ok {
		return nil
	}
	delete(u.claims, username)
	if time.Now().After(entry.expiresAt) {
		return nil
	}
	id := entry.ownerID
	return &id
}

func (u *usecase) HandleManagedBotWebhook(
	ctx context.Context, secret, headerSecret string, payload ManagedBotWebhookPayload,
) error {
	expected := os.Getenv("MANAGER_BOT_WEBHOOK_SECRET")
	if expected == "" || secret != expected || headerSecret != expected {
		return httpx.Forbidden("managed_bot.invalid_secret", "invalid webhook secret")
	}
	if payload.Bot.ID == 0 {
		return nil // no managed_bot payload -> ack and do nothing, mirrors Django
	}

	managerToken := os.Getenv("MANAGER_BOT_TOKEN")
	if managerToken == "" {
		return nil
	}
	token, err := u.fetchToken(ctx, managerToken, payload.Bot.ID)
	if err != nil || token == "" {
		// Django logs and still returns 200 to Telegram; a missing token here
		// just means this update carries no new info to persist.
		return nil
	}

	encrypted, err := u.cipher.Encrypt([]byte(token))
	if err != nil {
		return httpx.Internal(fmt.Errorf("encrypt managed bot token: %w", err))
	}

	ownerID := u.resolveClaim(payload.Bot.Username)
	_, err = u.repo.UpsertManagedBot(ctx, UpsertManagedBotInput{
		OwnerID:        ownerID,
		TgBotID:        payload.Bot.ID,
		Username:       payload.Bot.Username,
		Name:           payload.Bot.FirstName,
		TokenEncrypted: encrypted,
	})
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// getManagedBotTokenResponse is Telegram's Bot API envelope for
// getManagedBotToken. result is either a bare token string or an object
// carrying one, depending on API version — Django handles both, so do we.
type getManagedBotTokenResponse struct {
	OK     bool `json:"ok"`
	Result any  `json:"result"`
}

func (u *usecase) fetchManagedBotToken(ctx context.Context, managerToken string, botUserID int64) (string, error) {
	reqURL := fmt.Sprintf(
		"https://api.telegram.org/bot%s/getManagedBotToken?user_id=%d", managerToken, botUserID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("build getManagedBotToken request: %w", err)
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call getManagedBotToken: %w", err)
	}
	defer resp.Body.Close()

	var body getManagedBotTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode getManagedBotToken response: %w", err)
	}
	switch v := body.Result.(type) {
	case string:
		return v, nil
	case map[string]any:
		if t, ok := v["token"].(string); ok {
			return t, nil
		}
	}
	return "", nil
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
