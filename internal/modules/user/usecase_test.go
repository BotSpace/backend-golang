package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
type fakeRepo struct {
	byPhone map[string]*User
	byID    map[uuid.UUID]*User
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byPhone: map[string]*User{}, byID: map[uuid.UUID]*User{}}
}

// Create emulates the SQL upsert-by-phone: a pending phone is updated in place
// (the usecase already rejects a *validated* phone before calling Create).
func (r *fakeRepo) Create(_ context.Context, u *User) error {
	if prev, ok := r.byPhone[u.Phone]; ok {
		u.ID = prev.ID // ON CONFLICT keeps the existing row's id
	}
	cp := *u
	r.byPhone[u.Phone] = &cp
	r.byID[cp.ID] = &cp
	return nil
}
func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*User, error) {
	if u, ok := r.byID[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, database.ErrNotFound
}
func (r *fakeRepo) GetByPhone(_ context.Context, phone string) (*User, error) {
	if u, ok := r.byPhone[phone]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, database.ErrNotFound
}
func (r *fakeRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	for _, u := range r.byID {
		if u.Email == email {
			cp := *u
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}
func (r *fakeRepo) MarkValidated(_ context.Context, id uuid.UUID) (*User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	now := time.Now()
	u.ValidatedAt = &now
	cp := *u
	return &cp, nil
}
func (r *fakeRepo) UpdateProfile(_ context.Context, id uuid.UUID, _ ProfileUpdate) (*User, error) {
	return r.GetByID(context.Background(), id)
}
func (r *fakeRepo) UpdatePassword(_ context.Context, id uuid.UUID, hash string) error {
	u, ok := r.byID[id]
	if !ok {
		return database.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}
func (r *fakeRepo) UpdateRole(_ context.Context, id uuid.UUID, role string) (*User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	u.Role = role
	cp := *u
	return &cp, nil
}
func (r *fakeRepo) AddBalance(_ context.Context, id uuid.UUID, delta int64) (*User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if u.Balance+delta < 0 {
		return nil, ErrInsufficientFunds
	}
	u.Balance += delta
	cp := *u
	return &cp, nil
}
func (r *fakeRepo) SetActive(_ context.Context, id uuid.UUID, active bool) (*User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	u.IsActive = active
	cp := *u
	return &cp, nil
}
func (r *fakeRepo) RecordBalanceHistory(_ context.Context, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (r *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.byID[id]; !ok {
		return database.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}
func (r *fakeRepo) List(_ context.Context, _ ListFilter) ([]User, error) { return nil, nil }
func (r *fakeRepo) Count(_ context.Context, _ ListFilter) (int64, error)  { return 0, nil }

func (r *fakeRepo) GetSubscription(_ context.Context, _ uuid.UUID) (*Subscription, error) {
	return nil, nil
}
func (r *fakeRepo) ListProjectsBrief(_ context.Context, _ uuid.UUID) ([]ProjectBrief, error) {
	return nil, nil
}
func (r *fakeRepo) CountProjectsForUser(_ context.Context, _ uuid.UUID) (int64, error) { return 0, nil }
func (r *fakeRepo) CountPurchasedTemplates(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *fakeRepo) CountAuthoredTemplates(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestRegisterThenValidatedPhoneIsTaken(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	ctx := context.Background()
	in := RegisterInput{Phone: "+998900000000", FirstName: "A", LastName: "B", Password: "password123"}

	u, err := uc.Register(ctx, in)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if u.IsValidated() {
		t.Fatal("newly registered user must be pending (validated_at nil)")
	}

	// Still pending -> re-register allowed (upsert semantics).
	if _, err := uc.Register(ctx, in); err != nil {
		t.Fatalf("re-register pending: %v", err)
	}

	// Confirm, then a second register must be rejected as a conflict.
	if _, err := uc.MarkValidated(ctx, u.ID); err != nil {
		t.Fatalf("mark validated: %v", err)
	}
	if _, err := uc.Register(ctx, in); status(err) != 409 {
		t.Fatalf("expected 409 for validated phone, got %v", err)
	}
}

func TestAuthenticateRequiresValidatedAndCorrectPassword(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	if _, err := uc.Register(ctx, RegisterInput{Phone: "+998911111111", Password: "secretpass"}); err != nil {
		t.Fatal(err)
	}

	// Not validated yet -> 403.
	if _, err := uc.Authenticate(ctx, "+998911111111", "secretpass"); status(err) != 403 {
		t.Fatalf("expected 403 for unvalidated login, got %v", err)
	}

	u, _ := uc.GetByPhone(ctx, "+998911111111")
	_, _ = uc.MarkValidated(ctx, u.ID)

	// Wrong password -> 401.
	if _, err := uc.Authenticate(ctx, "+998911111111", "wrong"); status(err) != 401 {
		t.Fatalf("expected 401 for wrong password, got %v", err)
	}
	// Correct -> ok.
	if _, err := uc.Authenticate(ctx, "+998911111111", "secretpass"); err != nil {
		t.Fatalf("valid login failed: %v", err)
	}

	// Sanity: stored hash actually verifies.
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("secretpass")) != nil {
		t.Fatal("stored password hash does not verify")
	}
	_ = errors.New
}
