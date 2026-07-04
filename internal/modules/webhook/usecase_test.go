package webhook

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
type fakeRepo struct {
	// projectOwners simulates the projects table: existence + ownership.
	projectOwners map[uuid.UUID]uuid.UUID
	projectHooks  map[uuid.UUID][2]uuid.UUID // [release, debug]

	endpoints map[string]*WebhookEndpoint // key: projectID|nodeID|chatID
	byToken   map[uuid.UUID]*WebhookEndpoint

	bots map[int64]*ManagedBot
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		projectOwners: map[uuid.UUID]uuid.UUID{},
		projectHooks:  map[uuid.UUID][2]uuid.UUID{},
		endpoints:     map[string]*WebhookEndpoint{},
		byToken:       map[uuid.UUID]*WebhookEndpoint{},
		bots:          map[int64]*ManagedBot{},
	}
}

func endpointKey(projectID uuid.UUID, nodeID string, chatID int64) string {
	return fmt.Sprintf("%s|%s|%d", projectID, nodeID, chatID)
}

func (r *fakeRepo) MintEndpoint(_ context.Context, in MintInput) (*WebhookEndpoint, error) {
	owner, ok := r.projectOwners[in.ProjectID]
	if !ok || owner != in.OwnerID {
		return nil, database.ErrNotFound
	}
	key := endpointKey(in.ProjectID, in.NodeID, in.ChatID)
	if e, ok := r.endpoints[key]; ok {
		cp := *e
		return &cp, nil
	}
	e := &WebhookEndpoint{
		ID:        uuid.New(),
		Token:     uuid.New(),
		ProjectID: in.ProjectID,
		NodeID:    in.NodeID,
		ChatID:    in.ChatID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	r.endpoints[key] = e
	r.byToken[e.Token] = e
	cp := *e
	return &cp, nil
}

func (r *fakeRepo) ResolveToken(_ context.Context, token uuid.UUID) (*ResolvedHook, error) {
	e, ok := r.byToken[token]
	if !ok {
		return nil, database.ErrNotFound
	}
	hooks := r.projectHooks[e.ProjectID]
	return &ResolvedHook{
		Token:              e.Token,
		ProjectID:          e.ProjectID,
		NodeID:             e.NodeID,
		ChatID:             e.ChatID,
		ReleaseWebhookUUID: hooks[0],
		DebugWebhookUUID:   hooks[1],
	}, nil
}

func (r *fakeRepo) ListManagedBotsForOwner(_ context.Context, ownerID uuid.UUID) ([]ManagedBot, error) {
	var out []ManagedBot
	for _, b := range r.bots {
		if b.OwnerID != nil && *b.OwnerID == ownerID {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (r *fakeRepo) UpsertManagedBot(_ context.Context, in UpsertManagedBotInput) (*ManagedBot, error) {
	b, ok := r.bots[in.TgBotID]
	if !ok {
		b = &ManagedBot{ID: uuid.New(), TgBotID: in.TgBotID, CreatedAt: time.Now()}
		r.bots[in.TgBotID] = b
	}
	b.OwnerID = in.OwnerID
	b.Username = in.Username
	b.Name = in.Name
	b.TokenEncrypted = in.TokenEncrypted
	b.UpdatedAt = time.Now()
	cp := *b
	return &cp, nil
}

func (r *fakeRepo) GetManagedBotByIDForOwner(_ context.Context, id, ownerID uuid.UUID) (*ManagedBot, error) {
	for _, b := range r.bots {
		if b.ID == id && b.OwnerID != nil && *b.OwnerID == ownerID {
			cp := *b
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) MarkManagedBotUsed(_ context.Context, id uuid.UUID) error {
	for _, b := range r.bots {
		if b.ID == id {
			b.Used = true
			return nil
		}
	}
	return nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New([]byte("unit-test-key-not-for-production"))
	if err != nil {
		t.Fatalf("build cipher: %v", err)
	}
	return c
}

func TestMintEndpointOwnershipAndIdempotency(t *testing.T) {
	repo := newFakeRepo()
	ownerID := uuid.New()
	strangerID := uuid.New()
	projectID := uuid.New()
	repo.projectOwners[projectID] = ownerID

	uc := NewUsecase(repo, testCipher(t))
	ctx := context.Background()

	in := MintInput{OwnerID: ownerID, ProjectID: projectID, NodeID: "node-1", ChatID: 0}
	first, err := uc.MintEndpoint(ctx, in)
	if err != nil {
		t.Fatalf("first mint: %v", err)
	}

	// Same (project, node_id, chat_id) -> the same endpoint (get-or-create).
	second, err := uc.MintEndpoint(ctx, in)
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if first.Token != second.Token {
		t.Fatalf("expected idempotent token, got %s != %s", first.Token, second.Token)
	}

	// A different chat_id mints a distinct endpoint.
	withChat, err := uc.MintEndpoint(ctx, MintInput{OwnerID: ownerID, ProjectID: projectID, NodeID: "node-1", ChatID: 42})
	if err != nil {
		t.Fatalf("mint with chat_id: %v", err)
	}
	if withChat.Token == first.Token {
		t.Fatal("expected a distinct token for a different chat_id")
	}

	// A non-owner must not be able to mint against someone else's project.
	if _, err := uc.MintEndpoint(ctx, MintInput{OwnerID: strangerID, ProjectID: projectID, NodeID: "node-1"}); status(err) != 404 {
		t.Fatalf("expected 404 for non-owner mint, got %v", err)
	}

	// node_id is required.
	if _, err := uc.MintEndpoint(ctx, MintInput{OwnerID: ownerID, ProjectID: projectID, NodeID: "  "}); status(err) != 400 {
		t.Fatalf("expected 400 for empty node_id, got %v", err)
	}
}

func TestResolveTokenGatedByInternalToken(t *testing.T) {
	t.Setenv("APP_INTERNAL_TOKEN", "internal-secret")

	repo := newFakeRepo()
	ownerID := uuid.New()
	projectID := uuid.New()
	release, debug := uuid.New(), uuid.New()
	repo.projectOwners[projectID] = ownerID
	repo.projectHooks[projectID] = [2]uuid.UUID{release, debug}

	uc := NewUsecase(repo, testCipher(t))
	ctx := context.Background()

	minted, err := uc.MintEndpoint(ctx, MintInput{OwnerID: ownerID, ProjectID: projectID, NodeID: "node-1"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	if _, err := uc.ResolveToken(ctx, "wrong-token", minted.Token); status(err) != 401 {
		t.Fatalf("expected 401 for wrong internal token, got %v", err)
	}
	if _, err := uc.ResolveToken(ctx, "internal-secret", uuid.New()); status(err) != 404 {
		t.Fatalf("expected 404 for unknown webhook token, got %v", err)
	}

	hook, err := uc.ResolveToken(ctx, "internal-secret", minted.Token)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if hook.ReleaseWebhookUUID != release || hook.DebugWebhookUUID != debug {
		t.Fatalf("resolved hook does not carry the project's release/debug uuids: %+v", hook)
	}
}

func TestManagedBotClaimIsConsumedByWebhook(t *testing.T) {
	t.Setenv("MANAGER_BOT_TOKEN", "manager-token")
	t.Setenv("MANAGER_BOT_WEBHOOK_SECRET", "wh-secret")

	repo := newFakeRepo()
	ownerID := uuid.New()
	cipher := testCipher(t)
	uc := NewUsecase(repo, cipher).(*usecase)
	uc.fetchToken = func(_ context.Context, managerToken string, botUserID int64) (string, error) {
		// The usecase fetches the token for any incoming bot (claim only
		// decides ownership); assert the manager token, accept any bot id.
		if managerToken != "manager-token" {
			t.Fatalf("unexpected manager token: %s", managerToken)
		}
		return "bot-token-abc", nil
	}
	ctx := context.Background()

	// Wrong secret is rejected outright.
	if err := uc.ClaimManagedBotUsername(ownerID, "MyBot"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	payload := ManagedBotWebhookPayload{}
	payload.Bot.ID = 555
	payload.Bot.Username = "mybot" // case-insensitive match against the claim
	payload.Bot.FirstName = "My Bot"

	if err := uc.HandleManagedBotWebhook(ctx, "wrong", "wh-secret", payload); status(err) != 403 {
		t.Fatalf("expected 403 for wrong path secret, got %v", err)
	}

	if err := uc.HandleManagedBotWebhook(ctx, "wh-secret", "wh-secret", payload); err != nil {
		t.Fatalf("handle webhook: %v", err)
	}

	bots, err := uc.ListManagedBots(ctx, ownerID)
	if err != nil {
		t.Fatalf("list managed bots: %v", err)
	}
	if len(bots) != 1 {
		t.Fatalf("expected the claimed bot to be attached to its owner, got %d bots", len(bots))
	}
	got := bots[0]
	if got.TgBotID != 555 || got.Username != "mybot" {
		t.Fatalf("unexpected bot persisted: %+v", got)
	}
	plain, err := cipher.Decrypt(got.TokenEncrypted)
	if err != nil || string(plain) != "bot-token-abc" {
		t.Fatalf("token not round-trippable through the cipher: %v %q", err, plain)
	}

	// The claim is single-use: a second webhook for a fresh bot under the
	// same username (no new claim) must not resolve an owner.
	payload.Bot.ID = 556
	if err := uc.HandleManagedBotWebhook(ctx, "wh-secret", "wh-secret", payload); err != nil {
		t.Fatalf("handle second webhook: %v", err)
	}
	unclaimed := repo.bots[556]
	if unclaimed.OwnerID != nil {
		t.Fatalf("expected the second bot to be unowned (claim already consumed), got owner %v", unclaimed.OwnerID)
	}
}

func TestResolveManagedBotToken(t *testing.T) {
	repo := newFakeRepo()
	cipher := testCipher(t)
	ownerID := uuid.New()
	strangerID := uuid.New()

	encrypted, err := cipher.Encrypt([]byte("123456:real-bot-token"))
	if err != nil {
		t.Fatalf("encrypt fixture token: %v", err)
	}
	bot := &ManagedBot{ID: uuid.New(), OwnerID: &ownerID, TgBotID: 42, Username: "mybot", TokenEncrypted: encrypted}
	repo.bots[bot.TgBotID] = bot

	uc := NewUsecase(repo, cipher)
	ctx := context.Background()

	// Owned bot resolves to its decrypted token and gets marked used.
	token, err := uc.ResolveManagedBotToken(ctx, bot.ID, ownerID)
	if err != nil {
		t.Fatalf("resolve managed bot token: %v", err)
	}
	if token != "123456:real-bot-token" {
		t.Fatalf("token = %q, want the decrypted fixture token", token)
	}
	if !repo.bots[bot.TgBotID].Used {
		t.Fatal("expected the managed bot to be marked used after resolution")
	}

	// A different owner (or an unknown id) must 404, not leak the token.
	if _, err := uc.ResolveManagedBotToken(ctx, bot.ID, strangerID); status(err) != 404 {
		t.Fatalf("expected 404 for a non-owned managed bot, got %v", err)
	}
	if _, err := uc.ResolveManagedBotToken(ctx, uuid.New(), ownerID); status(err) != 404 {
		t.Fatalf("expected 404 for an unknown managed bot id, got %v", err)
	}
}
