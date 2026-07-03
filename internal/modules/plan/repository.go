package plan

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
// domain errors.
type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) ListActivePlans(ctx context.Context) ([]Plan, error) {
	rows, err := r.queries.ListActivePlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active plans: %w", err)
	}
	out := make([]Plan, len(rows))
	for i, row := range rows {
		out[i] = planFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) GetPlanByID(ctx context.Context, id uuid.UUID) (*Plan, error) {
	return mapOnePlan(r.queries.GetPlanByID(ctx, id))
}

func (r *pgRepo) GetActivePlanByID(ctx context.Context, id uuid.UUID) (*Plan, error) {
	return mapOnePlan(r.queries.GetActivePlanByID(ctx, id))
}

func (r *pgRepo) GetPlanByName(ctx context.Context, name string) (*Plan, error) {
	return mapOnePlan(r.queries.GetPlanByName(ctx, name))
}

func (r *pgRepo) GetSubscriptionByUserID(ctx context.Context, userID uuid.UUID) (*Subscription, error) {
	return mapOneSubscription(r.queries.GetSubscriptionByUserID(ctx, userID))
}

func (r *pgRepo) GetOrCreateSubscription(ctx context.Context, userID, defaultPlanID uuid.UUID) (*Subscription, error) {
	return mapOneSubscription(r.queries.CreateSubscription(ctx, sqlcdb.CreateSubscriptionParams{
		ID:     uuid.New(),
		UserID: userID,
		PlanID: defaultPlanID,
		Status: StatusActive,
	}))
}

func (r *pgRepo) UpdateSubscriptionPlan(
	ctx context.Context, id, planID uuid.UUID, status string, expiresAt time.Time,
) (*Subscription, error) {
	return mapOneSubscription(r.queries.UpdateSubscriptionPlan(ctx, sqlcdb.UpdateSubscriptionPlanParams{
		ID:        id,
		PlanID:    planID,
		Status:    status,
		ExpiresAt: &expiresAt,
	}))
}

func (r *pgRepo) IncrementUsage(ctx context.Context, id uuid.UUID, inputDelta, outputDelta int64) error {
	rows, err := r.queries.IncrementSubscriptionUsage(ctx, sqlcdb.IncrementSubscriptionUsageParams{
		ID:          id,
		InputDelta:  inputDelta,
		OutputDelta: outputDelta,
	})
	if err != nil {
		return fmt.Errorf("increment subscription usage: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

// ===================== admin CRUD =====================

func (r *pgRepo) ListAllPlans(ctx context.Context) ([]Plan, error) {
	rows, err := r.queries.ListAllPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all plans: %w", err)
	}
	out := make([]Plan, len(rows))
	for i, row := range rows {
		out[i] = planFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CreatePlan(ctx context.Context, in CreatePlanInput) (*Plan, error) {
	price, err := stringToNumeric(in.Price)
	if err != nil {
		return nil, err
	}
	rate, err := stringToNumeric(in.CachedTokenRate)
	if err != nil {
		return nil, err
	}
	row, err := r.queries.CreatePlan(ctx, sqlcdb.CreatePlanParams{
		Name:            in.Name,
		Price:           price,
		Description:     strPtr(in.Description),
		IsTop:           in.IsTop,
		IsActive:        in.IsActive,
		SortOrder:       in.SortOrder,
		MaxBots:         in.MaxBots,
		MaxTokens:       in.MaxTokens,
		MaxOutputTokens: 0, // deprecated column, kept only for schema parity (see domain.go)
		MaxInputTokens:  0,
		CachedTokenRate: rate,
		Features:        featuresOrEmptyBytes(in.Features),
	})
	if err != nil {
		return nil, fmt.Errorf("create plan: %w", err)
	}
	p := planFromSQLC(row)
	return &p, nil
}

func (r *pgRepo) UpdatePlan(ctx context.Context, id uuid.UUID, in UpdatePlanInput) (*Plan, error) {
	var price, rate pgtype.Numeric
	if in.Price != nil {
		p, err := stringToNumeric(*in.Price)
		if err != nil {
			return nil, err
		}
		price = p
	}
	if in.CachedTokenRate != nil {
		rt, err := stringToNumeric(*in.CachedTokenRate)
		if err != nil {
			return nil, err
		}
		rate = rt
	}
	row, err := r.queries.UpdatePlanAdmin(ctx, sqlcdb.UpdatePlanAdminParams{
		ID:              id,
		Name:            in.Name,
		Price:           price,
		Description:     in.Description,
		IsTop:           in.IsTop,
		IsActive:        in.IsActive,
		SortOrder:       in.SortOrder,
		MaxBots:         in.MaxBots,
		MaxTokens:       in.MaxTokens,
		CachedTokenRate: rate,
		Features:        featuresOrNilBytes(in.Features),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		return nil, fmt.Errorf("update plan: %w", err)
	}
	p := planFromSQLC(row)
	return &p, nil
}

func (r *pgRepo) DeletePlan(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries.DeletePlan(ctx, id)
	if err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func mapOnePlan(row sqlcdb.Plan, err error) (*Plan, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query plan: %w", err)
	}
	p := planFromSQLC(row)
	return &p, nil
}

func mapOneSubscription(row sqlcdb.UserSubscription, err error) (*Subscription, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query subscription: %w", err)
	}
	s := subscriptionFromSQLC(row)
	return &s, nil
}

func planFromSQLC(r sqlcdb.Plan) Plan {
	return Plan{
		ID:              r.ID,
		Name:            r.Name,
		Price:           numericToString(r.Price),
		Description:     deref(r.Description),
		IsTop:           r.IsTop,
		IsActive:        r.IsActive,
		SortOrder:       r.SortOrder,
		MaxBots:         r.MaxBots,
		MaxTokens:       r.MaxTokens,
		MaxOutputTokens: r.MaxOutputTokens,
		MaxInputTokens:  r.MaxInputTokens,
		CachedTokenRate: numericToString(r.CachedTokenRate),
		Features:        featuresOrEmpty(r.Features),
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func subscriptionFromSQLC(r sqlcdb.UserSubscription) Subscription {
	return Subscription{
		ID:               r.ID,
		UserID:           r.UserID,
		PlanID:           r.PlanID,
		Status:           r.Status,
		StartedAt:        r.StartedAt,
		ExpiresAt:        r.ExpiresAt,
		TokensUsedInput:  r.TokensUsedInput,
		TokensUsedOutput: r.TokensUsedOutput,
		TokenResetAt:     r.TokenResetAt,
		BotsCount:        r.BotsCount,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

func featuresOrEmpty(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

// featuresOrEmptyBytes is featuresOrEmpty's counterpart for a create payload:
// a nil/empty features blob is stored as "{}" rather than SQL NULL.
func featuresOrEmptyBytes(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

// featuresOrNilBytes is used by partial updates: nil means "leave unchanged"
// (the query COALESCEs a NULL param), so an empty payload must stay nil, not
// become "{}" and overwrite existing features.
func featuresOrNilBytes(b json.RawMessage) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stringToNumeric parses a decimal string ("120000.00") into a pgtype.Numeric
// for a money/rate query param (mirrors internal/modules/template's helper
// of the same name/contract).
func stringToNumeric(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if s == "" {
		s = "0"
	}
	if err := n.ScanScientific(s); err != nil {
		return n, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return n, nil
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
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
