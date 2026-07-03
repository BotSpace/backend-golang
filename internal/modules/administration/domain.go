// Package administration ports Django's core.apps.administration domain:
// an append-only admin audit log plus read-only admin dashboard stats.
package administration

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditLog is an append-only record of a destructive admin action. It is
// intentionally denormalised — Target is a free-form "type:id" style string,
// not a foreign key — so deleting the affected object later never drops the
// history of what happened to it.
type AuditLog struct {
	ID        uuid.UUID
	ActorID   *uuid.UUID // nil for system-initiated actions
	Action    string     // dotted code, e.g. "user.ban", "withdrawal.approve"
	Target    string     // free-form target description, e.g. "user:<uuid>"
	Metadata  json.RawMessage
	IP        string
	CreatedAt time.Time
}

// AppendInput creates one audit log entry. Other modules call this (via the
// Usecase interface) right after a destructive admin operation commits.
type AppendInput struct {
	ActorID  *uuid.UUID
	Action   string
	Target   string
	Metadata json.RawMessage
	IP       string
}

// ListFilter narrows and paginates an audit log listing. Empty/nil fields
// mean "no filter" on that dimension.
type ListFilter struct {
	Action  string
	Target  string
	ActorID *uuid.UUID
	Limit   int32
	Offset  int32
}

// DailyCount is one point of a growth chart: a calendar day (YYYY-MM-DD, UTC)
// and the number of events on it.
type DailyCount struct {
	Day   string
	Count int64
}

// DashboardStats is the admin landing-page KPI snapshot. Users/projects come
// from this module's own tables; templates/withdrawals/revenue are computed
// via read-only cross-module SQL (queries/administration.sql) — no other
// module's Go package is imported, only its table is queried directly, per
// the porting guide's SQL-scoping rule.
type DashboardStats struct {
	UsersTotal             int64
	UsersActive            int64
	UsersNewToday          int64
	UsersNew7d             int64
	UsersNew30d            int64
	ProjectsTotal          int64
	ProjectsWithRelease    int64
	TemplatesTotal         int64
	TemplatesPendingVerify int64
	WithdrawalsPending     int64
	Revenue30d             string // NUMERIC rendered as a decimal string (so'm)
	DailySignups           []DailyCount
	DailyOrders            []DailyCount
}

// Repository is the persistence port. The usecase depends only on this
// interface, keeping storage swappable (postgres, in-memory tests).
type Repository interface {
	Append(ctx context.Context, in AppendInput) (*AuditLog, error)
	GetByID(ctx context.Context, id uuid.UUID) (*AuditLog, error)
	List(ctx context.Context, f ListFilter) ([]AuditLog, error)
	Count(ctx context.Context, f ListFilter) (int64, error)

	CountUsersTotal(ctx context.Context) (int64, error)
	CountUsersActive(ctx context.Context) (int64, error)
	CountUsersCreatedSince(ctx context.Context, since time.Time) (int64, error)
	CountProjectsTotal(ctx context.Context) (int64, error)
	CountProjectsWithRelease(ctx context.Context) (int64, error)
	CountTemplatesTotal(ctx context.Context) (int64, error)
	CountTemplateSubmissionsPending(ctx context.Context) (int64, error)
	CountBotWithdrawalsPending(ctx context.Context) (int64, error)
	CountTemplateWithdrawalsPending(ctx context.Context) (int64, error)
	SumBotCommissionSince(ctx context.Context, since time.Time) (string, error)
	SumTemplateCommissionSince(ctx context.Context, since time.Time) (string, error)
	DailyUserSignups(ctx context.Context, since time.Time) ([]DailyCount, error)
	DailyPaidOrders(ctx context.Context, since time.Time) ([]DailyCount, error)
}
