package administration

import (
	"context"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
type fakeRepo struct {
	entries       []AuditLog
	usersTotal    int64
	usersToday    int64
	projectsTotal int64
	daily         []DailyCount
}

func (r *fakeRepo) Append(_ context.Context, in AppendInput) (*AuditLog, error) {
	e := AuditLog{
		ID:        uuid.New(),
		ActorID:   in.ActorID,
		Action:    in.Action,
		Target:    in.Target,
		Metadata:  in.Metadata,
		IP:        in.IP,
		CreatedAt: time.Now(),
	}
	r.entries = append(r.entries, e)
	return &e, nil
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*AuditLog, error) {
	for _, e := range r.entries {
		if e.ID == id {
			cp := e
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) List(_ context.Context, f ListFilter) ([]AuditLog, error) {
	var out []AuditLog
	for _, e := range r.entries {
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (r *fakeRepo) Count(_ context.Context, f ListFilter) (int64, error) {
	items, _ := r.List(context.Background(), f)
	return int64(len(items)), nil
}

func (r *fakeRepo) CountUsersTotal(_ context.Context) (int64, error)  { return r.usersTotal, nil }
func (r *fakeRepo) CountUsersActive(_ context.Context) (int64, error) { return r.usersTotal, nil }
func (r *fakeRepo) CountUsersCreatedSince(_ context.Context, _ time.Time) (int64, error) {
	return r.usersToday, nil
}
func (r *fakeRepo) CountProjectsTotal(_ context.Context) (int64, error)              { return r.projectsTotal, nil }
func (r *fakeRepo) CountProjectsWithRelease(_ context.Context) (int64, error)        { return 0, nil }
func (r *fakeRepo) CountTemplatesTotal(_ context.Context) (int64, error)             { return 0, nil }
func (r *fakeRepo) CountTemplateSubmissionsPending(_ context.Context) (int64, error) { return 0, nil }
func (r *fakeRepo) CountBotWithdrawalsPending(_ context.Context) (int64, error)      { return 0, nil }
func (r *fakeRepo) CountTemplateWithdrawalsPending(_ context.Context) (int64, error) { return 0, nil }
func (r *fakeRepo) SumBotCommissionSince(_ context.Context, _ time.Time) (string, error) {
	return "0", nil
}
func (r *fakeRepo) SumTemplateCommissionSince(_ context.Context, _ time.Time) (string, error) {
	return "0", nil
}
func (r *fakeRepo) DailyUserSignups(_ context.Context, _ time.Time) ([]DailyCount, error) {
	return r.daily, nil
}
func (r *fakeRepo) DailyPaidOrders(_ context.Context, _ time.Time) ([]DailyCount, error) {
	return nil, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestLogActionThenGetAuditLog(t *testing.T) {
	uc := NewUsecase(&fakeRepo{})
	ctx := context.Background()

	actor := uuid.New()
	entry, err := uc.LogAction(ctx, AppendInput{ActorID: &actor, Action: "user.ban", Target: "user:123"})
	if err != nil {
		t.Fatalf("log action: %v", err)
	}
	if entry.Action != "user.ban" {
		t.Fatalf("expected action user.ban, got %q", entry.Action)
	}

	got, err := uc.GetAuditLog(ctx, entry.ID)
	if err != nil {
		t.Fatalf("get audit log: %v", err)
	}
	if got.Target != "user:123" {
		t.Fatalf("expected target user:123, got %q", got.Target)
	}
}

func TestLogActionRejectsEmptyAction(t *testing.T) {
	uc := NewUsecase(&fakeRepo{})
	if _, err := uc.LogAction(context.Background(), AppendInput{Action: ""}); status(err) != 400 {
		t.Fatalf("expected 400 for empty action, got %v", err)
	}
}

func TestGetAuditLogNotFound(t *testing.T) {
	uc := NewUsecase(&fakeRepo{})
	if _, err := uc.GetAuditLog(context.Background(), uuid.New()); status(err) != 404 {
		t.Fatalf("expected 404 for missing entry, got %v", err)
	}
}

func TestListAuditLogAppliesDefaultPagination(t *testing.T) {
	repo := &fakeRepo{}
	uc := NewUsecase(repo)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := uc.LogAction(ctx, AppendInput{Action: "user.ban"}); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := uc.ListAuditLog(ctx, ListFilter{Limit: 0, Offset: -5})
	if err != nil {
		t.Fatalf("list audit log: %v", err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("expected 3 entries, got total=%d items=%d", total, len(items))
	}
}

func TestDashboardStatsAggregatesRepoCounts(t *testing.T) {
	repo := &fakeRepo{
		usersTotal:    100,
		usersToday:    5,
		projectsTotal: 42,
		daily:         []DailyCount{{Day: "2026-07-01", Count: 2}, {Day: "2026-07-02", Count: 3}},
	}
	uc := NewUsecase(repo)

	stats, err := uc.DashboardStats(context.Background())
	if err != nil {
		t.Fatalf("dashboard stats: %v", err)
	}
	if stats.UsersTotal != 100 || stats.UsersNewToday != 5 || stats.ProjectsTotal != 42 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(stats.DailySignups) != 2 {
		t.Fatalf("expected 2 daily signup buckets, got %d", len(stats.DailySignups))
	}
}
