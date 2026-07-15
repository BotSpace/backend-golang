package user

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. sqlc handles SQL <-> Go mapping; this
// adapter only translates domain <-> persistence models and pgx errors into
// domain errors. The usecase never sees sqlc, so it stays trivially testable.
type pgRepo struct {
	queries *sqlcdb.Queries
	// pool is kept alongside the sqlc queries for the admin user listing,
	// whose dynamic filter set + whitelisted ORDER BY can't be expressed as a
	// static sqlc query — see List/Count below.
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool), pool: pool}
}

const uniqueViolation = "23505"

func (r *pgRepo) Create(ctx context.Context, u *User) error {
	row, err := r.queries.CreateUser(ctx, sqlcdb.CreateUserParams{
		ID:           u.ID,
		Phone:        strPtr(u.Phone),
		FirstName:    u.FirstName,
		LastName:     u.LastName,
		PasswordHash: u.PasswordHash,
		Role:         roleOrDefault(u.Role),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrPhoneTaken
		}
		return fmt.Errorf("create user: %w", err)
	}
	*u = fromSQLC(row)
	return nil
}

func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return mapOne(r.queries.GetUserByID(ctx, id))
}

func (r *pgRepo) GetByPhone(ctx context.Context, phone string) (*User, error) {
	return mapOne(r.queries.GetUserByPhone(ctx, strPtr(phone)))
}

func (r *pgRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	return mapOne(r.queries.GetUserByEmail(ctx, strPtr(email)))
}

func (r *pgRepo) MarkValidated(ctx context.Context, id uuid.UUID) (*User, error) {
	return mapOne(r.queries.MarkUserValidated(ctx, id))
}

func (r *pgRepo) UpdateProfile(ctx context.Context, id uuid.UUID, p ProfileUpdate) (*User, error) {
	return mapOne(r.queries.UpdateUserProfile(ctx, sqlcdb.UpdateUserProfileParams{
		ID:        id,
		FirstName: p.FirstName,
		LastName:  p.LastName,
		Username:  p.Username,
		Email:     p.Email,
	}))
}

func (r *pgRepo) UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error {
	rows, err := r.queries.UpdateUserPassword(ctx, sqlcdb.UpdateUserPasswordParams{ID: id, PasswordHash: hash})
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) UpdateRole(ctx context.Context, id uuid.UUID, role string) (*User, error) {
	return mapOne(r.queries.UpdateUserRole(ctx, sqlcdb.UpdateUserRoleParams{ID: id, Role: role}))
}

// AddBalance applies a signed delta. The SQL guards against going below zero;
// when the guard fails no row is returned (pgx.ErrNoRows) -> ErrInsufficientFunds.
func (r *pgRepo) AddBalance(ctx context.Context, id uuid.UUID, delta int64) (*User, error) {
	row, err := r.queries.AddUserBalance(ctx, sqlcdb.AddUserBalanceParams{ID: id, Delta: delta})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInsufficientFunds
	}
	return mapOne(row, err)
}

func (r *pgRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) (*User, error) {
	return mapOne(r.queries.SetUserActive(ctx, sqlcdb.SetUserActiveParams{ID: id, IsActive: active}))
}

// RecordBalanceHistory writes an audit row into payment's `history` table
// (queried by table name only, no cross-module Go import — see
// AdminInsertBalanceHistory in queries/users.sql). amount is signed: positive
// for a credit, negative for a debit; type mirrors payment's history.type
// convention ("income"/"expense").
func (r *pgRepo) RecordBalanceHistory(ctx context.Context, userID uuid.UUID, delta int64, comment string) error {
	historyType := "income"
	if delta < 0 {
		historyType = "expense"
	}
	amount, err := stringToNumeric(strconv.FormatInt(delta, 10))
	if err != nil {
		return err
	}
	_, err = r.queries.AdminInsertBalanceHistory(ctx, sqlcdb.AdminInsertBalanceHistoryParams{
		ID:      uuid.New(),
		UserID:  &userID,
		Amount:  amount,
		Comment: strPtr(comment),
		Type:    historyType,
	})
	if err != nil {
		return fmt.Errorf("record balance history: %w", err)
	}
	return nil
}

// listWhere builds the shared WHERE clause (and its positional args) for the
// admin user listing, used by both List and Count so the page and its total
// stay in sync. Args are appended to `args`; the returned string is either ""
// or "WHERE ...".
func listWhere(f ListFilter, args *[]any) string {
	var conds []string
	add := func(cond string, val any) {
		*args = append(*args, val)
		conds = append(conds, fmt.Sprintf(cond, len(*args)))
	}
	if f.Search != "" {
		*args = append(*args, "%"+f.Search+"%")
		n := len(*args)
		conds = append(conds, fmt.Sprintf(
			"(u.phone ILIKE $%[1]d OR u.first_name ILIKE $%[1]d OR u.last_name ILIKE $%[1]d OR u.email ILIKE $%[1]d)", n))
	}
	if f.Role != "" {
		add("u.role = $%d", f.Role)
	}
	if f.IsActive != nil {
		add("u.is_active = $%d", *f.IsActive)
	}
	if f.IsStaff != nil {
		add("u.is_staff = $%d", *f.IsStaff)
	}
	if f.Plan != "" {
		add("p.name = $%d", f.Plan)
	}
	if f.JoinedAfter != nil {
		add("u.created_at >= $%d", *f.JoinedAfter)
	}
	if f.JoinedBefore != nil {
		add("u.created_at <= $%d", *f.JoinedBefore)
	}
	if f.BalanceMin != nil {
		add("u.balance >= $%d", *f.BalanceMin)
	}
	if f.BalanceMax != nil {
		add("u.balance <= $%d", *f.BalanceMax)
	}
	if len(conds) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conds, " AND ")
}

// orderClause maps the whitelisted ordering keys the admin panel sends to
// safe SQL. An unknown/empty key falls back to newest-first. Never interpolates
// caller input into SQL — the switch is the whitelist.
func orderClause(ordering string) string {
	switch ordering {
	case "date_joined":
		return "ORDER BY u.created_at ASC"
	case "-date_joined":
		return "ORDER BY u.created_at DESC"
	case "balance":
		return "ORDER BY u.balance ASC"
	case "-balance":
		return "ORDER BY u.balance DESC"
	case "tokens_used_total":
		return "ORDER BY tokens_used_total ASC"
	case "-tokens_used_total":
		return "ORDER BY tokens_used_total DESC"
	case "id":
		return "ORDER BY u.id ASC"
	case "-id":
		return "ORDER BY u.id DESC"
	default:
		return "ORDER BY u.created_at DESC"
	}
}

func (r *pgRepo) List(ctx context.Context, f ListFilter) ([]User, error) {
	args := make([]any, 0, 12)
	where := listWhere(f, &args)
	args = append(args, f.Limit, f.Offset)
	query := fmt.Sprintf(`
		SELECT u.id, u.phone, u.first_name, u.last_name, u.username, u.email,
		       u.password_hash, u.role, u.balance, u.template_balance,
		       u.is_active, u.validated_at, u.kyc_verified_at, u.created_at, u.updated_at,
		       COALESCE(us.tokens_used_input + us.tokens_used_output, 0) AS tokens_used_total
		FROM users u
		LEFT JOIN user_subscription us ON us.user_id = u.id
		LEFT JOIN plan p ON p.id = us.plan_id
		%s
		%s
		LIMIT $%d OFFSET $%d`,
		where, orderClause(f.Ordering), len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var phone, username, email *string
		var templateBalance pgtype.Numeric
		if err := rows.Scan(
			&u.ID, &phone, &u.FirstName, &u.LastName, &username, &email,
			&u.PasswordHash, &u.Role, &u.Balance, &templateBalance,
			&u.IsActive, &u.ValidatedAt, &u.KYCVerifiedAt, &u.CreatedAt, &u.UpdatedAt,
			&u.TokensUsedTotal,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.Phone, u.Username, u.Email = deref(phone), deref(username), deref(email)
		u.TemplateBalance = numericToString(templateBalance)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return out, nil
}

func (r *pgRepo) Count(ctx context.Context, f ListFilter) (int64, error) {
	args := make([]any, 0, 10)
	where := listWhere(f, &args)
	query := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM users u
		LEFT JOIN user_subscription us ON us.user_id = u.id
		LEFT JOIN plan p ON p.id = us.plan_id
		%s`, where)
	var n int64
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (r *pgRepo) Delete(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries.DeleteUser(ctx, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) GetSubscription(ctx context.Context, userID uuid.UUID) (*Subscription, error) {
	row, err := r.queries.GetUserSubscriptionDetail(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user subscription: %w", err)
	}
	return &Subscription{
		Plan:             row.PlanID.String(),
		PlanName:         row.PlanName,
		Status:           row.Status,
		StartedAt:        row.StartedAt,
		ExpiresAt:        row.ExpiresAt,
		TokenResetAt:     row.TokenResetAt,
		BotsCount:        row.BotsCount,
		TokensUsedInput:  row.TokensUsedInput,
		TokensUsedOutput: row.TokensUsedOutput,
		TokenLimit:       row.MaxTokens,
		MaxBots:          row.MaxBots,
	}, nil
}

func (r *pgRepo) ListProjectsBrief(ctx context.Context, userID uuid.UUID) ([]ProjectBrief, error) {
	rows, err := r.queries.ListProjectsForUserBrief(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list projects for user: %w", err)
	}
	out := make([]ProjectBrief, len(rows))
	for i, row := range rows {
		out[i] = ProjectBrief{
			ID: row.ID, Name: row.Name, Username: deref(row.Username),
			IsTemplate: row.IsTemplate, CreatedAt: row.CreatedAt,
		}
	}
	return out, nil
}

func (r *pgRepo) CountProjectsForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := r.queries.CountProjectsForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count projects for user: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountPurchasedTemplates(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := r.queries.CountPurchasedTemplatesForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count purchased templates: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountAuthoredTemplates(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := r.queries.CountAuthoredTemplatesForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count authored templates: %w", err)
	}
	return n, nil
}

func mapOne(row sqlcdb.User, err error) (*User, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	u := fromSQLC(row)
	return &u, nil
}

func fromSQLC(r sqlcdb.User) User {
	return User{
		ID:              r.ID,
		Phone:           deref(r.Phone),
		FirstName:       r.FirstName,
		LastName:        r.LastName,
		Username:        deref(r.Username),
		Email:           deref(r.Email),
		PasswordHash:    r.PasswordHash,
		Role:            r.Role,
		Balance:         r.Balance,
		TemplateBalance: numericToString(r.TemplateBalance),
		IsActive:        r.IsActive,
		ValidatedAt:     r.ValidatedAt,
		KYCVerifiedAt:   r.KycVerifiedAt,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func roleOrDefault(role string) string {
	if role == "" {
		return RoleUser
	}
	return role
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

// stringToNumeric parses a decimal string into a pgtype.Numeric (mirrors
// internal/modules/template's helper of the same name/contract).
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
