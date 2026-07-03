package authbot

import (
	"context"
	"testing"
	"time"

	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// ---- fake authbot.Repository (in-memory, no DB required) ----

type fakeAuthbotRepo struct {
	botUsers map[uuid.UUID]*BotUser
	codes    map[uuid.UUID]*Code
	byValue  map[string]uuid.UUID
}

func newFakeAuthbotRepo() *fakeAuthbotRepo {
	return &fakeAuthbotRepo{
		botUsers: map[uuid.UUID]*BotUser{},
		codes:    map[uuid.UUID]*Code{},
		byValue:  map[string]uuid.UUID{},
	}
}

func (r *fakeAuthbotRepo) UpsertBotUser(_ context.Context, in IssueCodeInput) (*BotUser, error) {
	for _, bu := range r.botUsers {
		if bu.TelegramID == in.TelegramID && bu.Phone == in.Phone {
			bu.FirstName, bu.LastName, bu.Username = in.FirstName, in.LastName, in.Username
			cp := *bu
			return &cp, nil
		}
	}
	bu := &BotUser{
		ID: uuid.New(), TelegramID: in.TelegramID, Phone: in.Phone,
		FirstName: in.FirstName, LastName: in.LastName, Username: in.Username,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.botUsers[bu.ID] = bu
	cp := *bu
	return &cp, nil
}

// UpsertCode emulates the SQL CASE-based upsert: a still-valid code for the
// bot user is returned untouched; an expired (or absent) one is (re)created.
func (r *fakeAuthbotRepo) UpsertCode(_ context.Context, botUserID uuid.UUID, codeValue string, expiresAt time.Time) (*Code, error) {
	for _, c := range r.codes {
		if c.BotUserID != botUserID {
			continue
		}
		if time.Now().Before(c.ExpiresAt) {
			cp := *c
			return &cp, nil
		}
		delete(r.byValue, c.Value)
		if _, taken := r.byValue[codeValue]; taken {
			return nil, ErrCodeCollision
		}
		c.Value, c.ExpiresAt, c.MessageID = codeValue, expiresAt, nil
		r.byValue[codeValue] = c.ID
		cp := *c
		return &cp, nil
	}
	if _, taken := r.byValue[codeValue]; taken {
		return nil, ErrCodeCollision
	}
	c := &Code{ID: uuid.New(), Value: codeValue, BotUserID: botUserID, ExpiresAt: expiresAt, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	r.codes[c.ID] = c
	r.byValue[codeValue] = c.ID
	cp := *c
	return &cp, nil
}

func (r *fakeAuthbotRepo) SetCodeMessageID(_ context.Context, codeID uuid.UUID, messageID int64) error {
	c, ok := r.codes[codeID]
	if !ok {
		return database.ErrNotFound
	}
	c.MessageID = &messageID
	return nil
}

func (r *fakeAuthbotRepo) GetCodeWithBotUser(_ context.Context, value string) (*Code, *BotUser, error) {
	id, ok := r.byValue[value]
	if !ok {
		return nil, nil, database.ErrNotFound
	}
	c, ok := r.codes[id]
	if !ok {
		return nil, nil, database.ErrNotFound
	}
	bu, ok := r.botUsers[c.BotUserID]
	if !ok {
		return nil, nil, database.ErrNotFound
	}
	ccp, bcp := *c, *bu
	return &ccp, &bcp, nil
}

func (r *fakeAuthbotRepo) DeleteCode(_ context.Context, codeID uuid.UUID) error {
	c, ok := r.codes[codeID]
	if !ok {
		return database.ErrNotFound
	}
	delete(r.byValue, c.Value)
	delete(r.codes, codeID)
	return nil
}

// ---- fake user.Repository, wrapped in the real user.Usecase ----
// Mirrors internal/modules/user/usecase_test.go's fakeRepo so this module's
// tests exercise the real Register/GetByPhone/MarkValidated business rules.

type fakeUserRepo struct {
	byPhone map[string]*user.User
	byID    map[uuid.UUID]*user.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byPhone: map[string]*user.User{}, byID: map[uuid.UUID]*user.User{}}
}

func (r *fakeUserRepo) Create(_ context.Context, u *user.User) error {
	if prev, ok := r.byPhone[u.Phone]; ok {
		u.ID = prev.ID
	}
	cp := *u
	r.byPhone[u.Phone] = &cp
	r.byID[cp.ID] = &cp
	return nil
}
func (r *fakeUserRepo) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	if u, ok := r.byID[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, database.ErrNotFound
}
func (r *fakeUserRepo) GetByPhone(_ context.Context, phone string) (*user.User, error) {
	if u, ok := r.byPhone[phone]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, database.ErrNotFound
}
func (r *fakeUserRepo) MarkValidated(_ context.Context, id uuid.UUID) (*user.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	now := time.Now()
	u.ValidatedAt = &now
	r.byPhone[u.Phone] = u
	cp := *u
	return &cp, nil
}
func (r *fakeUserRepo) UpdateProfile(ctx context.Context, id uuid.UUID, _ user.ProfileUpdate) (*user.User, error) {
	return r.GetByID(ctx, id)
}
func (r *fakeUserRepo) UpdatePassword(_ context.Context, id uuid.UUID, hash string) error {
	u, ok := r.byID[id]
	if !ok {
		return database.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}
func (r *fakeUserRepo) UpdateRole(_ context.Context, id uuid.UUID, role string) (*user.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	u.Role = role
	cp := *u
	return &cp, nil
}
func (r *fakeUserRepo) AddBalance(_ context.Context, id uuid.UUID, delta int64) (*user.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	u.Balance += delta
	cp := *u
	return &cp, nil
}
func (r *fakeUserRepo) SetActive(_ context.Context, id uuid.UUID, active bool) (*user.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	u.IsActive = active
	cp := *u
	return &cp, nil
}
func (r *fakeUserRepo) RecordBalanceHistory(_ context.Context, _ uuid.UUID, _ int64, _ string) error {
	return nil
}
func (r *fakeUserRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.byID[id]; !ok {
		return database.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}
func (r *fakeUserRepo) List(_ context.Context, _ user.ListFilter) ([]user.User, error) {
	return nil, nil
}
func (r *fakeUserRepo) Count(_ context.Context, _ string) (int64, error) { return 0, nil }

func (r *fakeUserRepo) GetSubscription(_ context.Context, _ uuid.UUID) (*user.Subscription, error) {
	return nil, nil
}
func (r *fakeUserRepo) ListProjectsBrief(_ context.Context, _ uuid.UUID) ([]user.ProjectBrief, error) {
	return nil, nil
}
func (r *fakeUserRepo) CountProjectsForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *fakeUserRepo) CountPurchasedTemplates(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *fakeUserRepo) CountAuthoredTemplates(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

// ---- fake TokenIssuer ----

type fakeTokens struct{}

func (fakeTokens) IssueTokens(_ context.Context, userID uuid.UUID, _ string) (TokenPair, error) {
	return TokenPair{
		AccessToken:      "access-" + userID.String(),
		RefreshToken:     "refresh-" + userID.String(),
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		TokenType:        "Bearer",
	}, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func newTestUsecase() (*Usecase, *fakeAuthbotRepo, *fakeUserRepo) {
	authbotRepo := newFakeAuthbotRepo()
	userRepo := newFakeUserRepo()
	uc := NewUsecase(authbotRepo, user.NewUsecase(userRepo), fakeTokens{})
	return uc, authbotRepo, userRepo
}

func TestIssueCodeThenLoginCreatesAndValidatesUser(t *testing.T) {
	uc, _, userRepo := newTestUsecase()
	ctx := context.Background()

	issued, err := uc.IssueCode(ctx, IssueCodeInput{
		TelegramID: 123456, Phone: "+998900000000", FirstName: "Ali", LastName: "Valiyev",
	})
	if err != nil {
		t.Fatalf("issue code: %v", err)
	}
	if len(issued.Value) != codeDigits {
		t.Fatalf("expected a %d-digit code, got %q", codeDigits, issued.Value)
	}

	result, err := uc.Login(ctx, issued.Value)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if result.Phone != "+998900000000" || result.Tokens.AccessToken == "" {
		t.Fatalf("unexpected login result: %+v", result)
	}

	created, ok := userRepo.byPhone["+998900000000"]
	if !ok {
		t.Fatal("expected a user to be created by phone")
	}
	if created.ValidatedAt == nil {
		t.Fatal("telegram-confirmed user must be validated immediately")
	}

	// The code is consumed: a second login with the same value must fail.
	if _, err := uc.Login(ctx, issued.Value); status(err) != 400 {
		t.Fatalf("expected 400 replaying a consumed code, got %v", err)
	}
}

func TestLoginReusesExistingValidatedUser(t *testing.T) {
	uc, _, userRepo := newTestUsecase()
	ctx := context.Background()

	existing := &user.User{ID: uuid.New(), Phone: "+998911111111", Role: user.RoleUser}
	now := time.Now()
	existing.ValidatedAt = &now
	if err := userRepo.Create(ctx, existing); err != nil {
		t.Fatal(err)
	}
	if _, err := userRepo.MarkValidated(ctx, existing.ID); err != nil {
		t.Fatal(err)
	}

	issued, err := uc.IssueCode(ctx, IssueCodeInput{TelegramID: 999, Phone: "+998911111111"})
	if err != nil {
		t.Fatalf("issue code: %v", err)
	}
	result, err := uc.Login(ctx, issued.Value)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if result.UserID != existing.ID {
		t.Fatalf("expected login to resolve the pre-existing user %s, got %s", existing.ID, result.UserID)
	}
}

func TestLoginWithUnknownCodeIsBadRequest(t *testing.T) {
	uc, _, _ := newTestUsecase()
	if _, err := uc.Login(context.Background(), "000000"); status(err) != 400 {
		t.Fatalf("expected 400 for an unknown code, got %v", err)
	}
}

func TestLoginWithExpiredCodeIsRejected(t *testing.T) {
	uc, repo, _ := newTestUsecase()
	ctx := context.Background()

	issued, err := uc.IssueCode(ctx, IssueCodeInput{TelegramID: 42, Phone: "+998922222222"})
	if err != nil {
		t.Fatalf("issue code: %v", err)
	}
	// Force expiry directly in the fake store (simulates CodeTTL elapsing).
	repo.codes[issued.CodeID].ExpiresAt = time.Now().Add(-time.Minute)

	if _, err := uc.Login(ctx, issued.Value); status(err) != 400 {
		t.Fatalf("expected 400 for an expired code, got %v", err)
	}
}

func TestSetMessageIDUnknownCodeIsNotFound(t *testing.T) {
	uc, _, _ := newTestUsecase()
	if err := uc.SetMessageID(context.Background(), uuid.New(), 555); status(err) != 404 {
		t.Fatalf("expected 404 for an unknown code id, got %v", err)
	}
}
