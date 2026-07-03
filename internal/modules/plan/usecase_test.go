package plan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
type fakeRepo struct {
	plans       map[uuid.UUID]*Plan
	plansByName map[string]*Plan
	subs        map[uuid.UUID]*Subscription // keyed by user id
	subsByID    map[uuid.UUID]*Subscription
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		plans:       map[uuid.UUID]*Plan{},
		plansByName: map[string]*Plan{},
		subs:        map[uuid.UUID]*Subscription{},
		subsByID:    map[uuid.UUID]*Subscription{},
	}
}

func (r *fakeRepo) addPlan(p Plan) Plan {
	cp := p
	r.plans[cp.ID] = &cp
	r.plansByName[cp.Name] = &cp
	return cp
}

func (r *fakeRepo) ListActivePlans(_ context.Context) ([]Plan, error) {
	var out []Plan
	for _, p := range r.plans {
		if p.IsActive {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (r *fakeRepo) GetPlanByID(_ context.Context, id uuid.UUID) (*Plan, error) {
	if p, ok := r.plans[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) GetActivePlanByID(ctx context.Context, id uuid.UUID) (*Plan, error) {
	p, err := r.GetPlanByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !p.IsActive {
		return nil, database.ErrNotFound
	}
	return p, nil
}

func (r *fakeRepo) GetPlanByName(_ context.Context, name string) (*Plan, error) {
	if p, ok := r.plansByName[name]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) GetSubscriptionByUserID(_ context.Context, userID uuid.UUID) (*Subscription, error) {
	if s, ok := r.subs[userID]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) GetOrCreateSubscription(_ context.Context, userID, defaultPlanID uuid.UUID) (*Subscription, error) {
	if s, ok := r.subs[userID]; ok {
		cp := *s
		return &cp, nil
	}
	now := time.Now()
	s := &Subscription{
		ID:           uuid.New(),
		UserID:       userID,
		PlanID:       defaultPlanID,
		Status:       StatusActive,
		StartedAt:    now,
		TokenResetAt: now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	r.subs[userID] = s
	r.subsByID[s.ID] = s
	cp := *s
	return &cp, nil
}

func (r *fakeRepo) UpdateSubscriptionPlan(
	_ context.Context, id, planID uuid.UUID, status string, expiresAt time.Time,
) (*Subscription, error) {
	s, ok := r.subsByID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	s.PlanID = planID
	s.Status = status
	s.ExpiresAt = &expiresAt
	s.UpdatedAt = time.Now()
	cp := *s
	return &cp, nil
}

func (r *fakeRepo) IncrementUsage(_ context.Context, id uuid.UUID, inputDelta, outputDelta int64) error {
	s, ok := r.subsByID[id]
	if !ok {
		return database.ErrNotFound
	}
	s.TokensUsedInput += inputDelta
	s.TokensUsedOutput += outputDelta
	return nil
}

func (r *fakeRepo) ExpireActiveSubscriptions(_ context.Context) ([]Subscription, error) {
	now := time.Now()
	var moved []Subscription
	for _, s := range r.subsByID {
		if s.Status == StatusActive && s.ExpiresAt != nil && s.ExpiresAt.Before(now) {
			s.Status = StatusGracePeriod
			s.UpdatedAt = now
			moved = append(moved, *s)
		}
	}
	return moved, nil
}

func (r *fakeRepo) SuspendGracePeriodSubscriptions(_ context.Context) ([]Subscription, error) {
	cutoff := time.Now().Add(-3 * 24 * time.Hour)
	var moved []Subscription
	for _, s := range r.subsByID {
		if s.Status == StatusGracePeriod && s.ExpiresAt != nil && s.ExpiresAt.Before(cutoff) {
			s.Status = StatusSuspended
			s.UpdatedAt = time.Now()
			moved = append(moved, *s)
		}
	}
	return moved, nil
}

// ===================== admin CRUD (used by AdminXxxPlan tests, if any) =====================

func (r *fakeRepo) ListAllPlans(_ context.Context) ([]Plan, error) {
	out := make([]Plan, 0, len(r.plans))
	for _, p := range r.plans {
		out = append(out, *p)
	}
	return out, nil
}

func (r *fakeRepo) CreatePlan(_ context.Context, in CreatePlanInput) (*Plan, error) {
	p := Plan{
		ID:              uuid.New(),
		Name:            in.Name,
		Price:           in.Price,
		Description:     in.Description,
		IsTop:           in.IsTop,
		IsActive:        in.IsActive,
		SortOrder:       in.SortOrder,
		MaxBots:         in.MaxBots,
		MaxTokens:       in.MaxTokens,
		CachedTokenRate: in.CachedTokenRate,
		Features:        in.Features,
	}
	added := r.addPlan(p)
	return &added, nil
}

func (r *fakeRepo) UpdatePlan(_ context.Context, id uuid.UUID, in UpdatePlanInput) (*Plan, error) {
	p, ok := r.plans[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.Price != nil {
		p.Price = *in.Price
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.IsTop != nil {
		p.IsTop = *in.IsTop
	}
	if in.IsActive != nil {
		p.IsActive = *in.IsActive
	}
	if in.SortOrder != nil {
		p.SortOrder = *in.SortOrder
	}
	if in.MaxBots != nil {
		p.MaxBots = *in.MaxBots
	}
	if in.MaxTokens != nil {
		p.MaxTokens = *in.MaxTokens
	}
	if in.CachedTokenRate != nil {
		p.CachedTokenRate = *in.CachedTokenRate
	}
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) DeletePlan(_ context.Context, id uuid.UUID) error {
	if _, ok := r.plans[id]; !ok {
		return database.ErrNotFound
	}
	delete(r.plans, id)
	return nil
}

// fakeUsers is a minimal in-memory user.Usecase — only GetByID/AddBalance are
// exercised by the plan usecase; the rest panic if ever called.
type fakeUsers struct {
	balances map[uuid.UUID]int64
}

func newFakeUsers() *fakeUsers { return &fakeUsers{balances: map[uuid.UUID]int64{}} }

func (f *fakeUsers) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	bal, ok := f.balances[id]
	if !ok {
		return nil, httpx.NotFound("user.not_found", "user not found")
	}
	return &user.User{ID: id, Balance: bal}, nil
}

func (f *fakeUsers) AddBalance(_ context.Context, id uuid.UUID, delta int64) (*user.User, error) {
	bal, ok := f.balances[id]
	if !ok {
		return nil, httpx.NotFound("user.not_found", "user not found")
	}
	if bal+delta < 0 {
		return nil, httpx.Conflict("user.insufficient_funds", "insufficient balance")
	}
	bal += delta
	f.balances[id] = bal
	return &user.User{ID: id, Balance: bal}, nil
}

func (f *fakeUsers) Register(context.Context, user.RegisterInput) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) Authenticate(context.Context, string, string) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) GetByPhone(context.Context, string) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) GetByEmail(context.Context, string) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) MarkValidated(context.Context, uuid.UUID) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) UpdateProfile(context.Context, uuid.UUID, user.ProfileUpdate) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) ChangePassword(context.Context, uuid.UUID, string, string) error {
	return errors.New("not implemented")
}
func (f *fakeUsers) ResetPassword(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (f *fakeUsers) SetRole(context.Context, uuid.UUID, string) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) Delete(context.Context, uuid.UUID) error { return errors.New("not implemented") }
func (f *fakeUsers) List(context.Context, user.ListFilter) ([]user.User, int64, error) {
	return nil, 0, errors.New("not implemented")
}
func (f *fakeUsers) GetAdminDetail(context.Context, uuid.UUID) (*user.AdminDetail, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) Ban(context.Context, uuid.UUID) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) Unban(context.Context, uuid.UUID) (*user.User, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeUsers) AdminAdjustBalance(context.Context, uuid.UUID, int64, string) (*user.User, error) {
	return nil, errors.New("not implemented")
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestCurrentCreatesFreeSubscriptionOnFirstAccess(t *testing.T) {
	repo := newFakeRepo()
	free := repo.addPlan(Plan{ID: uuid.New(), Name: NameFree, Price: "0", IsActive: true, MaxBots: 1, MaxTokens: 1000000})
	uc := NewUsecase(repo, newFakeUsers())
	userID := uuid.New()

	sub, p, err := uc.Current(context.Background(), userID)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if p.ID != free.ID {
		t.Fatalf("expected new subscription on FREE plan, got %s", p.Name)
	}
	if sub.UserID != userID {
		t.Fatal("subscription not scoped to the caller")
	}

	// Second call must reuse the same subscription (idempotent get-or-create).
	sub2, _, err := uc.Current(context.Background(), userID)
	if err != nil {
		t.Fatalf("Current (2nd): %v", err)
	}
	if sub2.ID != sub.ID {
		t.Fatal("expected the same subscription row on repeat access")
	}
}

func TestUpgradeChargesBalanceAndSwitchesPlan(t *testing.T) {
	repo := newFakeRepo()
	repo.addPlan(Plan{ID: uuid.New(), Name: NameFree, Price: "0", IsActive: true, MaxBots: 1, MaxTokens: 1000000})
	pro := repo.addPlan(Plan{ID: uuid.New(), Name: NamePro, Price: "100000", IsActive: true, MaxBots: 5, MaxTokens: 5000000})

	users := newFakeUsers()
	userID := uuid.New()
	users.balances[userID] = 150000

	uc := NewUsecase(repo, users)
	ctx := context.Background()
	if _, _, err := uc.Current(ctx, userID); err != nil { // seeds the FREE subscription
		t.Fatal(err)
	}

	sub, newPlan, previousPlanName, err := uc.Upgrade(ctx, userID, pro.ID, 1)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if newPlan.ID != pro.ID || sub.PlanID != pro.ID {
		t.Fatal("subscription was not switched to the purchased plan")
	}
	if previousPlanName != NameFree {
		t.Fatalf("expected previous plan %q, got %q", NameFree, previousPlanName)
	}
	if got := users.balances[userID]; got != 50000 {
		t.Fatalf("expected balance 150000-100000=50000, got %d", got)
	}
}

func TestUpgradeInsufficientBalanceLeavesPlanUnchanged(t *testing.T) {
	repo := newFakeRepo()
	free := repo.addPlan(Plan{ID: uuid.New(), Name: NameFree, Price: "0", IsActive: true, MaxBots: 1, MaxTokens: 1000000})
	pro := repo.addPlan(Plan{ID: uuid.New(), Name: NamePro, Price: "100000", IsActive: true, MaxBots: 5, MaxTokens: 5000000})

	users := newFakeUsers()
	userID := uuid.New()
	users.balances[userID] = 100 // far short of the plan price

	uc := NewUsecase(repo, users)
	ctx := context.Background()
	if _, _, err := uc.Current(ctx, userID); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := uc.Upgrade(ctx, userID, pro.ID, 1); status(err) != 409 {
		t.Fatalf("expected 409 insufficient funds, got %v", err)
	}
	sub, err := repo.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.PlanID != free.ID {
		t.Fatal("plan must not change when the balance charge fails")
	}
	if got := users.balances[userID]; got != 100 {
		t.Fatalf("balance must be untouched on failure, got %d", got)
	}
}

func TestReportUsageAppliesCachedTokenDiscount(t *testing.T) {
	repo := newFakeRepo()
	repo.addPlan(Plan{
		ID: uuid.New(), Name: NameFree, Price: "0", IsActive: true,
		MaxBots: 1, MaxTokens: 1000000, CachedTokenRate: "0.50",
	})
	users := newFakeUsers()
	userID := uuid.New()
	users.balances[userID] = 0

	uc := NewUsecase(repo, users)
	ctx := context.Background()

	billable, err := uc.ReportUsage(ctx, ReportUsageInput{
		UserID: userID, InputTokens: 100, OutputTokens: 20, CacheReadTokens: 40,
	})
	if err != nil {
		t.Fatalf("ReportUsage: %v", err)
	}
	// uncached = 100-40 = 60; cached billable = round(40*0.5) = 20; total = 80.
	if billable != 80 {
		t.Fatalf("expected billable input 80, got %d", billable)
	}

	sub, err := repo.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.TokensUsedInput != 80 || sub.TokensUsedOutput != 20 {
		t.Fatalf("usage not persisted correctly: input=%d output=%d", sub.TokensUsedInput, sub.TokensUsedOutput)
	}
}

func TestReportUsageUnknownUserIsNotFound(t *testing.T) {
	repo := newFakeRepo()
	repo.addPlan(Plan{ID: uuid.New(), Name: NameFree, Price: "0", IsActive: true, MaxBots: 1, MaxTokens: 1000000})
	uc := NewUsecase(repo, newFakeUsers())

	if _, err := uc.ReportUsage(context.Background(), ReportUsageInput{UserID: uuid.New(), InputTokens: 1}); status(err) != 404 {
		t.Fatalf("expected 404 for unknown user, got %v", err)
	}
}

// TestExpireSubscriptions covers the scheduler's net-new expiry job (see
// queries/plan.sql's ExpireActiveSubscriptions doc comment for why this
// isn't a Django port): active -> grace_period once expires_at passes,
// grace_period -> suspended a further 3 days after that. Subscriptions with
// no expiry, or not yet due, must be left untouched.
func TestExpireSubscriptions(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, newFakeUsers())
	ctx := context.Background()
	now := time.Now()

	pastExpiry := now.Add(-time.Hour)
	futureExpiry := now.Add(time.Hour)
	longPastExpiry := now.Add(-4 * 24 * time.Hour) // >3 days ago

	activeExpired := &Subscription{ID: uuid.New(), UserID: uuid.New(), Status: StatusActive, ExpiresAt: &pastExpiry}
	activeNotYet := &Subscription{ID: uuid.New(), UserID: uuid.New(), Status: StatusActive, ExpiresAt: &futureExpiry}
	activeNoExpiry := &Subscription{ID: uuid.New(), UserID: uuid.New(), Status: StatusActive, ExpiresAt: nil}
	graceDueForSuspend := &Subscription{ID: uuid.New(), UserID: uuid.New(), Status: StatusGracePeriod, ExpiresAt: &longPastExpiry}
	graceStillWithinWindow := &Subscription{ID: uuid.New(), UserID: uuid.New(), Status: StatusGracePeriod, ExpiresAt: &pastExpiry}

	for _, s := range []*Subscription{activeExpired, activeNotYet, activeNoExpiry, graceDueForSuspend, graceStillWithinWindow} {
		repo.subsByID[s.ID] = s
	}

	toGrace, suspended, err := uc.ExpireSubscriptions(ctx)
	if err != nil {
		t.Fatalf("ExpireSubscriptions: %v", err)
	}
	if toGrace != 1 {
		t.Fatalf("expected 1 subscription moved to grace_period, got %d", toGrace)
	}
	if suspended != 1 {
		t.Fatalf("expected 1 subscription suspended, got %d", suspended)
	}
	if repo.subsByID[activeExpired.ID].Status != StatusGracePeriod {
		t.Error("expired active subscription should move to grace_period")
	}
	if repo.subsByID[activeNotYet.ID].Status != StatusActive {
		t.Error("not-yet-expired active subscription must stay active")
	}
	if repo.subsByID[activeNoExpiry.ID].Status != StatusActive {
		t.Error("subscription with no expiry must stay active")
	}
	if repo.subsByID[graceDueForSuspend.ID].Status != StatusSuspended {
		t.Error("grace_period subscription past the 3-day window should be suspended")
	}
	if repo.subsByID[graceStillWithinWindow.ID].Status != StatusGracePeriod {
		t.Error("grace_period subscription still within the 3-day window must stay grace_period")
	}
}
