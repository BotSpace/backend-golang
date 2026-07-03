package administration

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// chartDays is how far back the dashboard growth chart looks.
const chartDays = 30

// Usecase exposes business operations for the administration module. Other
// modules record an admin action via LogAction — pass the concrete Usecase
// (not this package's internals) the same way auth consumes user.Usecase.
type Usecase interface {
	LogAction(ctx context.Context, in AppendInput) (*AuditLog, error)
	GetAuditLog(ctx context.Context, id uuid.UUID) (*AuditLog, error)
	ListAuditLog(ctx context.Context, f ListFilter) ([]AuditLog, int64, error)
	DashboardStats(ctx context.Context) (*DashboardStats, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

// LogAction records one admin action. Unlike Django's log_admin_action, this
// does surface errors to the caller — silently swallowing a failed audit
// write is the caller's decision to make, not this module's.
func (u *usecase) LogAction(ctx context.Context, in AppendInput) (*AuditLog, error) {
	if in.Action == "" {
		return nil, httpx.BadRequest("audit.action_required", "action is required")
	}
	entry, err := u.repo.Append(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return entry, nil
}

func (u *usecase) GetAuditLog(ctx context.Context, id uuid.UUID) (*AuditLog, error) {
	entry, err := u.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("audit.not_found", "audit log entry not found")
		}
		return nil, httpx.Internal(err)
	}
	return entry, nil
}

// ListAuditLog returns a page of audit log entries plus the total count
// matching the filter.
func (u *usecase) ListAuditLog(ctx context.Context, f ListFilter) ([]AuditLog, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.List(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.Count(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// DashboardStats computes the admin landing-page KPIs: users (total/active/
// new today/7d/30d), projects (total/with a release), the template catalog
// and pending review queue, pending withdrawals (bot + template payout
// requests combined), 30-day platform revenue (bot order commission +
// template marketplace commission), and daily signup/paid-order growth
// charts.
func (u *usecase) DashboardStats(ctx context.Context) (*DashboardStats, error) {
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since7d := todayStart.AddDate(0, 0, -7)
	since30d := todayStart.AddDate(0, 0, -30)
	chartSince := todayStart.AddDate(0, 0, -chartDays)

	usersTotal, err := u.repo.CountUsersTotal(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	usersActive, err := u.repo.CountUsersActive(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	usersNewToday, err := u.repo.CountUsersCreatedSince(ctx, todayStart)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	usersNew7d, err := u.repo.CountUsersCreatedSince(ctx, since7d)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	usersNew30d, err := u.repo.CountUsersCreatedSince(ctx, since30d)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	projectsTotal, err := u.repo.CountProjectsTotal(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	projectsWithRelease, err := u.repo.CountProjectsWithRelease(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	templatesTotal, err := u.repo.CountTemplatesTotal(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	templatesPending, err := u.repo.CountTemplateSubmissionsPending(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	botWithdrawalsPending, err := u.repo.CountBotWithdrawalsPending(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	templateWithdrawalsPending, err := u.repo.CountTemplateWithdrawalsPending(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	botCommission, err := u.repo.SumBotCommissionSince(ctx, since30d)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	templateCommission, err := u.repo.SumTemplateCommissionSince(ctx, since30d)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	dailySignups, err := u.repo.DailyUserSignups(ctx, chartSince)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	dailyOrders, err := u.repo.DailyPaidOrders(ctx, chartSince)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	return &DashboardStats{
		UsersTotal:             usersTotal,
		UsersActive:            usersActive,
		UsersNewToday:          usersNewToday,
		UsersNew7d:             usersNew7d,
		UsersNew30d:            usersNew30d,
		ProjectsTotal:          projectsTotal,
		ProjectsWithRelease:    projectsWithRelease,
		TemplatesTotal:         templatesTotal,
		TemplatesPendingVerify: templatesPending,
		WithdrawalsPending:     botWithdrawalsPending + templateWithdrawalsPending,
		Revenue30d:             addMoney(botCommission, templateCommission),
		DailySignups:           dailySignups,
		DailyOrders:            dailyOrders,
	}, nil
}

// addMoney adds two decimal money strings using exact integer-cent
// arithmetic (avoids float rounding). Non-numeric input is treated as zero.
func addMoney(a, b string) string {
	ac, aOK := parseMoneyCents(a)
	bc, bOK := parseMoneyCents(b)
	if !aOK {
		ac = 0
	}
	if !bOK {
		bc = 0
	}
	sum := ac + bc
	neg := sum < 0
	if neg {
		sum = -sum
	}
	s := fmt.Sprintf("%d.%02d", sum/100, sum%100)
	if neg {
		s = "-" + s
	}
	return s
}

// parseMoneyCents parses a NUMERIC(20,2)-shaped decimal string ("12345.67")
// into an integer count of hundredths (cents/tiyin) and whether it parsed.
func parseMoneyCents(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	frac := "00"
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			frac = frac[:2]
		}
		for len(frac) < 2 {
			frac += "0"
		}
	}
	wholeN, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	fracN, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	cents := wholeN*100 + fracN
	if neg {
		cents = -cents
	}
	return cents, true
}
