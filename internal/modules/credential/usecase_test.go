package credential

import (
	"context"
	"testing"

	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
type fakeRepo struct {
	byID     map[uuid.UUID]*Credential
	projects map[uuid.UUID]uuid.UUID // project id -> owner id
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byID: map[uuid.UUID]*Credential{}, projects: map[uuid.UUID]uuid.UUID{}}
}

func (r *fakeRepo) Create(_ context.Context, in CreateInput) (*Credential, error) {
	c := &Credential{
		ID:            uuid.New(),
		OwnerID:       in.OwnerID,
		TypeKey:       in.TypeKey,
		Name:          in.Name,
		DataEncrypted: in.DataEncrypted,
	}
	r.byID[c.ID] = c
	cp := *c
	return &cp, nil
}

func (r *fakeRepo) GetForOwner(_ context.Context, id, ownerID uuid.UUID) (*Credential, error) {
	c, ok := r.byID[id]
	if !ok || c.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Credential, error) {
	c, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (r *fakeRepo) GetByOwnerAndName(_ context.Context, ownerID uuid.UUID, name string) (*Credential, error) {
	for _, c := range r.byID {
		if c.OwnerID == ownerID && c.Name == name {
			cp := *c
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) Update(_ context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Credential, error) {
	c, ok := r.byID[id]
	if !ok || c.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.DataEncrypted != nil {
		c.DataEncrypted = *in.DataEncrypted
	}
	cp := *c
	return &cp, nil
}

func (r *fakeRepo) SoftDelete(_ context.Context, id, ownerID uuid.UUID) error {
	c, ok := r.byID[id]
	if !ok || c.OwnerID != ownerID {
		return database.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}

func (r *fakeRepo) List(_ context.Context, f ListFilter) ([]Credential, error) {
	var out []Credential
	for _, c := range r.byID {
		if c.OwnerID == f.OwnerID && (f.TypeKey == "" || c.TypeKey == f.TypeKey) {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (r *fakeRepo) Count(_ context.Context, ownerID uuid.UUID, typeKey string) (int64, error) {
	items, _ := r.List(context.Background(), ListFilter{OwnerID: ownerID, TypeKey: typeKey})
	return int64(len(items)), nil
}

func (r *fakeRepo) GetProjectOwner(_ context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	ownerID, ok := r.projects[projectID]
	if !ok {
		return uuid.Nil, database.ErrNotFound
	}
	return ownerID, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func newTestUsecase() (*fakeRepo, Usecase) {
	repo := newFakeRepo()
	cipher, err := crypto.New([]byte("test-key-for-credential-usecase"))
	if err != nil {
		panic(err)
	}
	return repo, NewUsecase(repo, cipher)
}

func TestCreateRejectsMissingRequiredField(t *testing.T) {
	_, uc := newTestUsecase()
	ctx := context.Background()
	owner := uuid.New()

	_, err := uc.Create(ctx, CreateData{
		OwnerID: owner,
		TypeKey: "openai",
		Name:    "my-openai",
		Data:    map[string]any{}, // missing required api_key
	})
	if status(err) != 400 {
		t.Fatalf("expected 400 for missing required field, got %v", err)
	}
}

func TestCreateRejectsUnknownType(t *testing.T) {
	_, uc := newTestUsecase()
	ctx := context.Background()

	_, err := uc.Create(ctx, CreateData{OwnerID: uuid.New(), TypeKey: "does-not-exist", Name: "x", Data: map[string]any{}})
	if status(err) != 400 {
		t.Fatalf("expected 400 for unknown type_key, got %v", err)
	}
}

// TestCreateThenGetRoundTripsAndMasks verifies the encrypt-on-write /
// decrypt-on-read cycle, and that toResponse masks the secret field while
// InternalGetByID (the engine path) returns the plaintext.
func TestCreateThenGetRoundTripsAndMasks(t *testing.T) {
	_, uc := newTestUsecase()
	ctx := context.Background()
	owner := uuid.New()

	created, err := uc.Create(ctx, CreateData{
		OwnerID: owner,
		TypeKey: "openai",
		Name:    "my-openai",
		Data:    map[string]any{"api_key": "sk-1234567890"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Data["api_key"] != "sk-1234567890" {
		t.Fatalf("usecase-level Data must be plaintext, got %v", created.Data["api_key"])
	}

	// Owner-facing projection must mask the secret.
	resp := toResponse(created)
	if resp.Data["api_key"] != "••••7890" {
		t.Fatalf("expected masked api_key, got %v", resp.Data["api_key"])
	}

	// Data at rest must not equal the plaintext (actually encrypted).
	if created.DataEncrypted == `{"api_key":"sk-1234567890"}` {
		t.Fatal("data_encrypted must not be plaintext JSON")
	}

	// Owner Get decrypts the same plaintext back.
	got, err := uc.Get(ctx, created.ID, owner)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Data["api_key"] != "sk-1234567890" {
		t.Fatalf("get did not round-trip decryption, got %v", got.Data["api_key"])
	}

	// Wrong owner -> 404 (never leak existence to another user).
	if _, err := uc.Get(ctx, created.ID, uuid.New()); status(err) != 404 {
		t.Fatalf("expected 404 for cross-owner get, got %v", err)
	}

	// Internal-by-id (no project scoping) must return the same plaintext.
	internal, err := uc.InternalGetByID(ctx, created.ID, nil)
	if err != nil {
		t.Fatalf("internal get by id: %v", err)
	}
	if internal.Data["api_key"] != "sk-1234567890" {
		t.Fatalf("internal get did not decrypt, got %v", internal.Data["api_key"])
	}
}

// TestUpdateKeepsOldSecretWhenBlank mirrors Django's PatchSerializer merge
// behaviour: an empty/omitted secret field in the PATCH body keeps the old
// stored value, while non-secret fields are always overwritten.
func TestUpdateKeepsOldSecretWhenBlank(t *testing.T) {
	_, uc := newTestUsecase()
	ctx := context.Background()
	owner := uuid.New()

	created, err := uc.Create(ctx, CreateData{
		OwnerID: owner,
		TypeKey: "openai",
		Name:    "my-openai",
		Data:    map[string]any{"api_key": "sk-original", "base_url": "https://old.example.com"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := uc.Update(ctx, created.ID, owner, UpdateData{
		Data: map[string]any{"api_key": "", "base_url": "https://new.example.com"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Data["api_key"] != "sk-original" {
		t.Fatalf("expected old secret to be kept, got %v", updated.Data["api_key"])
	}
	if updated.Data["base_url"] != "https://new.example.com" {
		t.Fatalf("expected non-secret field to be overwritten, got %v", updated.Data["base_url"])
	}
}

// TestInternalGetByNameCrossTenantGuard mirrors credential_by_name: the owner
// is derived from X-Project-Id, so a caller can only resolve credentials
// owned by that project's owner.
func TestInternalGetByNameCrossTenantGuard(t *testing.T) {
	repo, uc := newTestUsecase()
	ctx := context.Background()
	owner := uuid.New()
	project := uuid.New()
	repo.projects[project] = owner

	if _, err := uc.Create(ctx, CreateData{
		OwnerID: owner,
		TypeKey: "telegram",
		Name:    "prod-bot",
		Data:    map[string]any{"bot_token": "123:ABC"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := uc.InternalGetByName(ctx, "prod-bot", project)
	if err != nil {
		t.Fatalf("internal get by name: %v", err)
	}
	if got.Data["bot_token"] != "123:ABC" {
		t.Fatalf("expected decrypted bot_token, got %v", got.Data["bot_token"])
	}

	// Unknown project -> 403 (Django returns 403 when the project is missing).
	if _, err := uc.InternalGetByName(ctx, "prod-bot", uuid.New()); status(err) != 403 {
		t.Fatalf("expected 403 for unknown project, got %v", err)
	}
}
