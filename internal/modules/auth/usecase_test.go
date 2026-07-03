package auth_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/botspace/backend/internal/modules/auth"
	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// ---- fake user.Repository (in-memory), wrapped in the real user.Usecase —
// same pattern internal/modules/authbot/usecase_test.go uses, so GoogleLogin
// (auth.Usecase) is exercised through the real user-module business logic
// (Register/UpdateProfile/MarkValidated), not a hand-rolled shortcut. ----

type fakeUserRepo struct {
	byID    map[uuid.UUID]*user.User
	byEmail map[string]uuid.UUID
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: map[uuid.UUID]*user.User{}, byEmail: map[string]uuid.UUID{}}
}

func (r *fakeUserRepo) Create(_ context.Context, u *user.User) error {
	cp := *u
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

// GetByPhone mirrors Postgres NULL semantics for the nullable `phone` column:
// an empty phone (Google-only accounts) never matches anything, even itself.
func (r *fakeUserRepo) GetByPhone(_ context.Context, phone string) (*user.User, error) {
	if phone == "" {
		return nil, database.ErrNotFound
	}
	for _, u := range r.byID {
		if u.Phone == phone {
			cp := *u
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeUserRepo) GetByEmail(_ context.Context, email string) (*user.User, error) {
	if id, ok := r.byEmail[email]; ok {
		cp := *r.byID[id]
		return &cp, nil
	}
	return nil, database.ErrNotFound
}

func (r *fakeUserRepo) MarkValidated(_ context.Context, id uuid.UUID) (*user.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if u.ValidatedAt == nil {
		now := time.Now()
		u.ValidatedAt = &now
	}
	cp := *u
	return &cp, nil
}

func (r *fakeUserRepo) UpdateProfile(_ context.Context, id uuid.UUID, p user.ProfileUpdate) (*user.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if p.FirstName != nil {
		u.FirstName = *p.FirstName
	}
	if p.LastName != nil {
		u.LastName = *p.LastName
	}
	if p.Username != nil {
		u.Username = *p.Username
	}
	if p.Email != nil {
		u.Email = *p.Email
		r.byEmail[*p.Email] = id
	}
	cp := *u
	return &cp, nil
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

// ---- fake RefreshStore ----

type fakeRefreshStore struct{ active map[uuid.UUID]bool }

func newFakeRefreshStore() *fakeRefreshStore {
	return &fakeRefreshStore{active: map[uuid.UUID]bool{}}
}

func (s *fakeRefreshStore) Save(_ context.Context, jti, _ uuid.UUID, _ time.Time) error {
	s.active[jti] = true
	return nil
}
func (s *fakeRefreshStore) IsActive(_ context.Context, jti uuid.UUID) (bool, error) {
	return s.active[jti], nil
}
func (s *fakeRefreshStore) Revoke(_ context.Context, jti uuid.UUID) error {
	s.active[jti] = false
	return nil
}
func (s *fakeRefreshStore) RevokeAllForUser(_ context.Context, _ uuid.UUID) error { return nil }

// ---- fake GoogleVerifier — stands in for a real call to Google's tokeninfo
// endpoint so these tests never touch the network. ----

type fakeGoogleVerifier struct {
	claims auth.GoogleClaims
	err    error
}

func (f fakeGoogleVerifier) Verify(_ context.Context, _ string) (auth.GoogleClaims, error) {
	return f.claims, f.err
}

// newTestUsecase builds a real auth.Usecase against fakes/in-memory state —
// only the RS256 keys are real (auto-generated into a temp dir by LoadKeys in
// "development" mode, same as keys_test.go), so issued tokens are genuinely
// signed and verifiable.
func newTestUsecase(t *testing.T, google auth.GoogleVerifier) (*auth.Usecase, *auth.TokenIssuer, *fakeUserRepo) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.JWTConfig{
		PrivateKeyPath: filepath.Join(dir, "priv.pem"),
		PublicKeyPath:  filepath.Join(dir, "pub.pem"),
		AccessTTL:      15 * time.Minute,
		RefreshTTL:     168 * time.Hour,
		Issuer:         "auth-test",
	}
	priv, pub, err := auth.LoadKeys(cfg, "development")
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	tokens := auth.NewTokenIssuer(priv, pub, cfg)

	userRepo := newFakeUserRepo()
	usersUC := user.NewUsecase(userRepo)
	// otp (nil) is never touched by GoogleLogin.
	uc := auth.NewUsecase(usersUC, tokens, nil, newFakeRefreshStore(), google)
	return uc, tokens, userRepo
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestGoogleLoginCreatesNewUserOnFirstSignIn(t *testing.T) {
	verifier := fakeGoogleVerifier{claims: auth.GoogleClaims{
		Email: "new.user@example.com", EmailVerified: true,
		FirstName: "New", LastName: "User", Sub: "google-sub-1",
	}}
	uc, tokens, userRepo := newTestUsecase(t, verifier)
	ctx := context.Background()

	got, err := uc.GoogleLogin(ctx, auth.GoogleLoginRequest{IDToken: "whatever"})
	if err != nil {
		t.Fatalf("GoogleLogin: %v", err)
	}
	if got.AccessToken == "" || got.RefreshToken == "" {
		t.Fatal("expected non-empty tokens")
	}

	created, err := userRepo.GetByEmail(ctx, "new.user@example.com")
	if err != nil {
		t.Fatalf("expected user to be created by email: %v", err)
	}
	if created.ValidatedAt == nil {
		t.Error("expected google-created user to be validated immediately")
	}
	if created.FirstName != "New" || created.LastName != "User" {
		t.Errorf("unexpected name: %+v", created)
	}
	if created.Phone != "" {
		t.Errorf("expected no phone on a google-only account, got %q", created.Phone)
	}

	// The access token really verifies and names this user.
	gotID, _, err := tokens.VerifyAccessToken(got.AccessToken)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if gotID != created.ID.String() {
		t.Errorf("access token subject = %s, want %s", gotID, created.ID)
	}
}

func TestGoogleLoginReusesExistingUserByEmail(t *testing.T) {
	verifier := fakeGoogleVerifier{claims: auth.GoogleClaims{
		Email: "existing@example.com", EmailVerified: true, FirstName: "Existing",
	}}
	uc, tokens, userRepo := newTestUsecase(t, verifier)
	ctx := context.Background()

	existingID := uuid.New()
	if err := userRepo.Create(ctx, &user.User{ID: existingID, FirstName: "Existing", Role: user.RoleUser}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	email := "existing@example.com"
	if _, err := userRepo.UpdateProfile(ctx, existingID, user.ProfileUpdate{Email: &email}); err != nil {
		t.Fatalf("seed email: %v", err)
	}

	got, err := uc.GoogleLogin(ctx, auth.GoogleLoginRequest{IDToken: "whatever"})
	if err != nil {
		t.Fatalf("GoogleLogin: %v", err)
	}

	if len(userRepo.byID) != 1 {
		t.Fatalf("expected no new user to be created, got %d users", len(userRepo.byID))
	}
	gotID, _, err := tokens.VerifyAccessToken(got.AccessToken)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if gotID != existingID.String() {
		t.Errorf("access token subject = %s, want the pre-existing user %s", gotID, existingID)
	}
}

func TestGoogleLoginRejectsUnverifiedEmail(t *testing.T) {
	verifier := fakeGoogleVerifier{claims: auth.GoogleClaims{
		Email: "unverified@example.com", EmailVerified: false,
	}}
	uc, _, _ := newTestUsecase(t, verifier)

	_, err := uc.GoogleLogin(context.Background(), auth.GoogleLoginRequest{IDToken: "whatever"})
	if got := status(err); got != 403 {
		t.Fatalf("status = %d, want 403 (forbidden); err=%v", got, err)
	}
}

func TestGoogleLoginPropagatesVerifierFailure(t *testing.T) {
	verifier := fakeGoogleVerifier{err: errFakeInvalidToken}
	uc, _, _ := newTestUsecase(t, verifier)

	_, err := uc.GoogleLogin(context.Background(), auth.GoogleLoginRequest{IDToken: "garbage"})
	if got := status(err); got != 401 {
		t.Fatalf("status = %d, want 401 (unauthorized); err=%v", got, err)
	}
}

func TestGoogleLoginWithoutClientIDConfiguredFailsCleanly(t *testing.T) {
	uc, _, _ := newTestUsecase(t, nil) // no GoogleVerifier wired — mirrors APP_GOOGLE_CLIENT_ID unset

	_, err := uc.GoogleLogin(context.Background(), auth.GoogleLoginRequest{IDToken: "whatever"})
	if err == nil {
		t.Fatal("expected an error when google sign-in is not configured")
	}
	if got := status(err); got != 500 {
		t.Fatalf("status = %d, want 500 (internal/config error); err=%v", got, err)
	}
}

var errFakeInvalidToken = fakeVerifyError("id_token signature invalid")

type fakeVerifyError string

func (e fakeVerifyError) Error() string { return string(e) }
