package administration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. sqlc handles SQL <-> Go mapping; this
// adapter only translates domain <-> persistence models and pgx errors into
// domain errors. The usecase never sees sqlc, so it stays trivially testable.
type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) Append(ctx context.Context, in AppendInput) (*AuditLog, error) {
	row, err := r.queries.AppendAuditLog(ctx, sqlcdb.AppendAuditLogParams{
		ActorID:  in.ActorID,
		Action:   in.Action,
		Target:   in.Target,
		Metadata: metadataOrNil(in.Metadata),
		Ip:       strPtr(in.IP),
	})
	if err != nil {
		return nil, fmt.Errorf("append audit log: %w", err)
	}
	a := fromSQLC(row)
	return &a, nil
}

func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*AuditLog, error) {
	row, err := r.queries.GetAuditLogByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	a := fromSQLC(row)
	return &a, nil
}

func (r *pgRepo) List(ctx context.Context, f ListFilter) ([]AuditLog, error) {
	rows, err := r.queries.ListAuditLog(ctx, sqlcdb.ListAuditLogParams{
		Action:  strPtr(f.Action),
		Target:  strPtr(f.Target),
		ActorID: f.ActorID,
		Limit:   f.Limit,
		Offset:  f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	out := make([]AuditLog, len(rows))
	for i, row := range rows {
		out[i] = fromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) Count(ctx context.Context, f ListFilter) (int64, error) {
	n, err := r.queries.CountAuditLog(ctx, sqlcdb.CountAuditLogParams{
		Action:  strPtr(f.Action),
		Target:  strPtr(f.Target),
		ActorID: f.ActorID,
	})
	if err != nil {
		return 0, fmt.Errorf("count audit log: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountUsersTotal(ctx context.Context) (int64, error) {
	n, err := r.queries.CountUsersTotal(ctx)
	if err != nil {
		return 0, fmt.Errorf("count users total: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountUsersCreatedSince(ctx context.Context, since time.Time) (int64, error) {
	n, err := r.queries.CountUsersCreatedSince(ctx, since)
	if err != nil {
		return 0, fmt.Errorf("count users created since: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountProjectsTotal(ctx context.Context) (int64, error) {
	n, err := r.queries.CountProjectsTotal(ctx)
	if err != nil {
		return 0, fmt.Errorf("count projects total: %w", err)
	}
	return n, nil
}

func (r *pgRepo) DailyUserSignups(ctx context.Context, since time.Time) ([]DailyCount, error) {
	rows, err := r.queries.DailyUserSignups(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("daily user signups: %w", err)
	}
	out := make([]DailyCount, len(rows))
	for i, row := range rows {
		out[i] = DailyCount{Day: row.Day, Count: row.Count}
	}
	return out, nil
}

func (r *pgRepo) CountUsersActive(ctx context.Context) (int64, error) {
	n, err := r.queries.CountUsersActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("count users active: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountProjectsWithRelease(ctx context.Context) (int64, error) {
	n, err := r.queries.CountProjectsWithRelease(ctx)
	if err != nil {
		return 0, fmt.Errorf("count projects with release: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountTemplatesTotal(ctx context.Context) (int64, error) {
	n, err := r.queries.CountTemplatesTotal(ctx)
	if err != nil {
		return 0, fmt.Errorf("count templates total: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountTemplateSubmissionsPending(ctx context.Context) (int64, error) {
	n, err := r.queries.CountTemplateSubmissionsPending(ctx)
	if err != nil {
		return 0, fmt.Errorf("count template submissions pending: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountBotWithdrawalsPending(ctx context.Context) (int64, error) {
	n, err := r.queries.CountBotWithdrawalsPending(ctx)
	if err != nil {
		return 0, fmt.Errorf("count bot withdrawals pending: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountTemplateWithdrawalsPending(ctx context.Context) (int64, error) {
	n, err := r.queries.CountTemplateWithdrawalsPending(ctx)
	if err != nil {
		return 0, fmt.Errorf("count template withdrawals pending: %w", err)
	}
	return n, nil
}

func (r *pgRepo) SumBotCommissionSince(ctx context.Context, since time.Time) (string, error) {
	n, err := r.queries.SumBotCommissionSince(ctx, since)
	if err != nil {
		return "0", fmt.Errorf("sum bot commission since: %w", err)
	}
	return numericToString(n), nil
}

func (r *pgRepo) SumTemplateCommissionSince(ctx context.Context, since time.Time) (string, error) {
	n, err := r.queries.SumTemplateCommissionSince(ctx, since)
	if err != nil {
		return "0", fmt.Errorf("sum template commission since: %w", err)
	}
	return numericToString(n), nil
}

func (r *pgRepo) DailyPaidOrders(ctx context.Context, since time.Time) ([]DailyCount, error) {
	rows, err := r.queries.DailyPaidOrders(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("daily paid orders: %w", err)
	}
	out := make([]DailyCount, len(rows))
	for i, row := range rows {
		out[i] = DailyCount{Day: row.Day, Count: row.Count}
	}
	return out, nil
}

// numericToString renders a pgtype.Numeric as a plain decimal string. An
// invalid/NULL value renders as "0".
func numericToString(n pgtype.Numeric) string {
	if !n.Valid {
		return "0"
	}
	v, err := n.Value()
	if err != nil || v == nil {
		return "0"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func fromSQLC(r sqlcdb.AuditLog) AuditLog {
	return AuditLog{
		ID:        r.ID,
		ActorID:   r.ActorID,
		Action:    r.Action,
		Target:    r.Target,
		Metadata:  json.RawMessage(r.Metadata),
		IP:        deref(r.Ip),
		CreatedAt: r.CreatedAt,
	}
}

// metadataOrNil normalises an empty payload to a NULL jsonb column instead of
// storing an empty byte slice.
func metadataOrNil(m json.RawMessage) []byte {
	if len(m) == 0 {
		return nil
	}
	return m
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
