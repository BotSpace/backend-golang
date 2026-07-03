package payment

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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
//
// pool is kept alongside queries (not just the pool-bound Queries) because
// ApplyBalanceDelta/ApproveWithdrawal need a real transaction: the History
// insert and the users.balance update must commit or roll back together.
type pgRepo struct {
	queries *sqlcdb.Queries
	pool    *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool), pool: pool}
}

// ===================== transactions =====================

func (r *pgRepo) CreateTransaction(ctx context.Context, in CreateTransactionInput) (*Transaction, error) {
	amountNum, err := stringToNumeric(in.Amount)
	if err != nil {
		return nil, err
	}
	row, err := r.queries.CreatePaymentTransaction(ctx, sqlcdb.CreatePaymentTransactionParams{
		ID:             in.ID,
		UserID:         in.UserID,
		Amount:         amountNum,
		IdempotencyKey: strPtr(in.IdempotencyKey),
	})
	if err != nil {
		return nil, fmt.Errorf("create payment transaction: %w", err)
	}
	t := transactionFromSQLC(row)
	return &t, nil
}

func (r *pgRepo) GetTransactionByID(ctx context.Context, id uuid.UUID) (*Transaction, error) {
	return mapTransaction(r.queries.GetPaymentTransactionByID(ctx, id))
}

func (r *pgRepo) GetTransactionForUser(ctx context.Context, id, userID uuid.UUID) (*Transaction, error) {
	return mapTransaction(r.queries.GetPaymentTransactionForUser(ctx, sqlcdb.GetPaymentTransactionForUserParams{
		ID: id, UserID: userID,
	}))
}

func (r *pgRepo) ListTransactionsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]Transaction, error) {
	rows, err := r.queries.ListPaymentTransactionsForUser(ctx, sqlcdb.ListPaymentTransactionsForUserParams{
		UserID: userID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list payment transactions: %w", err)
	}
	out := make([]Transaction, len(rows))
	for i, row := range rows {
		out[i] = transactionFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountTransactionsForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := r.queries.CountPaymentTransactionsForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count payment transactions: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListTransactionsAdmin(ctx context.Context, limit, offset int32) ([]Transaction, error) {
	rows, err := r.queries.ListPaymentTransactionsAdmin(ctx, sqlcdb.ListPaymentTransactionsAdminParams{
		Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list payment transactions admin: %w", err)
	}
	out := make([]Transaction, len(rows))
	for i, row := range rows {
		t := transactionFromSQLC(row.Transaction)
		t.UserPhone = deref(row.UserPhone)
		out[i] = t
	}
	return out, nil
}

func (r *pgRepo) CountTransactionsAdmin(ctx context.Context) (int64, error) {
	n, err := r.queries.CountPaymentTransactionsAdmin(ctx)
	if err != nil {
		return 0, fmt.Errorf("count payment transactions admin: %w", err)
	}
	return n, nil
}

func (r *pgRepo) SetTransactionStatus(ctx context.Context, id uuid.UUID, from, to bool) error {
	rows, err := r.queries.SetPaymentTransactionStatus(ctx, sqlcdb.SetPaymentTransactionStatusParams{
		ID: id, Status: to, ExpectedStatus: from,
	})
	if err != nil {
		return fmt.Errorf("set transaction status: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) SetTransactionExternalRef(ctx context.Context, id uuid.UUID, ref string) error {
	rows, err := r.queries.SetPaymentTransactionExternalRef(ctx, sqlcdb.SetPaymentTransactionExternalRefParams{
		ID: id, IdempotencyKey: strPtr(ref),
	})
	if err != nil {
		return fmt.Errorf("set transaction external ref: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) GetTransactionByExternalRef(ctx context.Context, ref string) (*Transaction, error) {
	return mapTransaction(r.queries.GetPaymentTransactionByExternalRef(ctx, strPtr(ref)))
}

// ===================== orders =====================

func (r *pgRepo) CreateOrder(ctx context.Context, in CreateOrderParams) (*Order, error) {
	amountNum, err := stringToNumeric(in.Amount)
	if err != nil {
		return nil, err
	}
	commissionNum, err := stringToNumeric(in.Commission)
	if err != nil {
		return nil, err
	}
	netNum, err := stringToNumeric(in.NetAmount)
	if err != nil {
		return nil, err
	}
	row, err := r.queries.CreatePaymentOrder(ctx, sqlcdb.CreatePaymentOrderParams{
		ID:             in.ID,
		ProjectID:      in.ProjectID,
		OwnerID:        in.OwnerID,
		PayerChatID:    in.PayerChatID,
		PayerFirstName: strPtr(in.PayerFirstName),
		PayerLastName:  strPtr(in.PayerLastName),
		PayerUsername:  strPtr(in.PayerUsername),
		Amount:         amountNum,
		Commission:     commissionNum,
		NetAmount:      netNum,
		Provider:       in.Provider,
		Comment:        strPtr(in.Comment),
		IdempotencyKey: strPtr(in.IdempotencyKey),
		TransactionID:  in.TransactionID,
		IsDebug:        in.IsDebug,
	})
	if err != nil {
		return nil, fmt.Errorf("create payment order: %w", err)
	}
	o := orderFromSQLC(row)
	return &o, nil
}

func (r *pgRepo) GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	return mapOrder(r.queries.GetPaymentOrderByID(ctx, id))
}

func (r *pgRepo) GetOrderForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Order, error) {
	return mapOrder(r.queries.GetPaymentOrderForOwner(ctx, sqlcdb.GetPaymentOrderForOwnerParams{
		ID: id, OwnerID: ownerID,
	}))
}

func (r *pgRepo) GetOrderByIdempotencyKey(ctx context.Context, key string) (*Order, error) {
	return mapOrder(r.queries.GetPaymentOrderByIdempotencyKey(ctx, strPtr(key)))
}

func (r *pgRepo) GetOrderByTransactionID(ctx context.Context, txnID uuid.UUID) (*Order, error) {
	return mapOrder(r.queries.GetPaymentOrderByTransactionID(ctx, &txnID))
}

func (r *pgRepo) AttachTransactionToOrder(ctx context.Context, orderID, txnID uuid.UUID) (*Order, error) {
	return mapOrder(r.queries.AttachPaymentTransactionToOrder(ctx, sqlcdb.AttachPaymentTransactionToOrderParams{
		ID: orderID, TransactionID: &txnID,
	}))
}

func (r *pgRepo) ClearOrderIdempotencyKey(ctx context.Context, orderID uuid.UUID) error {
	rows, err := r.queries.ClearPaymentOrderIdempotencyKey(ctx, orderID)
	if err != nil {
		return fmt.Errorf("clear order idempotency key: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) SetOrderPayInstruction(ctx context.Context, orderID uuid.UUID, externalID, payInstruction string) (*Order, error) {
	return mapOrder(r.queries.SetPaymentOrderPayInstruction(ctx, sqlcdb.SetPaymentOrderPayInstructionParams{
		ID: orderID, ExternalID: strPtr(externalID), PayInstruction: payInstruction,
	}))
}

func (r *pgRepo) MarkOrderPaid(ctx context.Context, id uuid.UUID, commission, netAmount, externalID string) (*Order, error) {
	commissionNum, err := stringToNumeric(commission)
	if err != nil {
		return nil, err
	}
	netNum, err := stringToNumeric(netAmount)
	if err != nil {
		return nil, err
	}
	return mapOrder(r.queries.MarkPaymentOrderPaid(ctx, sqlcdb.MarkPaymentOrderPaidParams{
		ID: id, Commission: commissionNum, NetAmount: netNum, ExternalID: strPtr(externalID),
	}))
}

func (r *pgRepo) MarkOrderCancelled(ctx context.Context, id uuid.UUID) (*Order, error) {
	return mapOrder(r.queries.MarkPaymentOrderCancelled(ctx, id))
}

func (r *pgRepo) ListPaidOrdersForOwner(ctx context.Context, f EarningsFilter) ([]Order, error) {
	rows, err := r.queries.ListPaidPaymentOrdersForOwner(ctx, sqlcdb.ListPaidPaymentOrdersForOwnerParams{
		OwnerID: f.OwnerID, ProjectID: f.ProjectID, Limit: f.Limit, Offset: f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list paid orders: %w", err)
	}
	out := make([]Order, len(rows))
	for i, row := range rows {
		out[i] = orderFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountPaidOrdersForOwner(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error) {
	n, err := r.queries.CountPaidPaymentOrdersForOwner(ctx, sqlcdb.CountPaidPaymentOrdersForOwnerParams{
		OwnerID: ownerID, ProjectID: projectID,
	})
	if err != nil {
		return 0, fmt.Errorf("count paid orders: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListOrdersAdmin(ctx context.Context, status string, limit, offset int32) ([]Order, error) {
	rows, err := r.queries.ListPaymentOrdersAdmin(ctx, sqlcdb.ListPaymentOrdersAdminParams{
		Status: strPtr(status), Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list payment orders admin: %w", err)
	}
	out := make([]Order, len(rows))
	for i, row := range rows {
		o := orderFromSQLC(row.PaymentOrder)
		o.ProjectName = row.ProjectName
		o.OwnerPhone = deref(row.OwnerPhone)
		out[i] = o
	}
	return out, nil
}

func (r *pgRepo) CountOrdersAdmin(ctx context.Context, status string) (int64, error) {
	n, err := r.queries.CountPaymentOrdersAdmin(ctx, strPtr(status))
	if err != nil {
		return 0, fmt.Errorf("count payment orders admin: %w", err)
	}
	return n, nil
}

func (r *pgRepo) SumEarningsForOwner(ctx context.Context, ownerID uuid.UUID) (string, string, error) {
	row, err := r.queries.SumPaymentEarningsForOwner(ctx, ownerID)
	if err != nil {
		return "0", "0", fmt.Errorf("sum earnings: %w", err)
	}
	return numericToString(row.TotalEarned), numericToString(row.TotalCommission), nil
}

func (r *pgRepo) EarningsByProject(ctx context.Context, ownerID uuid.UUID) ([]ProjectEarning, error) {
	rows, err := r.queries.PaymentEarningsByProject(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("earnings by project: %w", err)
	}
	out := make([]ProjectEarning, len(rows))
	for i, row := range rows {
		out[i] = ProjectEarning{
			ProjectID:   row.ProjectID,
			ProjectName: row.ProjectName,
			TotalEarned: numericToString(row.TotalEarned),
			OrderCount:  row.OrderCount,
		}
	}
	return out, nil
}

func (r *pgRepo) SumBotEarnedForOwner(ctx context.Context, ownerID uuid.UUID) (string, error) {
	n, err := r.queries.SumPaidPaymentOrderNetForOwner(ctx, ownerID)
	if err != nil {
		return "0", fmt.Errorf("sum bot earned: %w", err)
	}
	return numericToString(n), nil
}

func (r *pgRepo) GetProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, string, error) {
	p, err := r.queries.GetProject(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", database.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("get project owner: %w", err)
	}
	return p.OwnerID, p.Name, nil
}

// ===================== history =====================

func (r *pgRepo) ListHistoryForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]History, error) {
	rows, err := r.queries.ListPaymentHistoryForUser(ctx, sqlcdb.ListPaymentHistoryForUserParams{
		UserID: &userID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list payment history: %w", err)
	}
	out := make([]History, len(rows))
	for i, row := range rows {
		out[i] = historyFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountHistoryForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := r.queries.CountPaymentHistoryForUser(ctx, &userID)
	if err != nil {
		return 0, fmt.Errorf("count payment history: %w", err)
	}
	return n, nil
}

func (r *pgRepo) GetHistoryForUser(ctx context.Context, id, userID uuid.UUID) (*History, error) {
	return mapHistory(r.queries.GetPaymentHistoryForUser(ctx, sqlcdb.GetPaymentHistoryForUserParams{
		ID: id, UserID: &userID,
	}))
}

// ===================== withdrawals =====================

func (r *pgRepo) CreateWithdrawal(ctx context.Context, in CreateWithdrawalInput) (*WithdrawalRequest, error) {
	amountNum, err := stringToNumeric(in.Amount)
	if err != nil {
		return nil, err
	}
	row, err := r.queries.CreatePaymentWithdrawal(ctx, sqlcdb.CreatePaymentWithdrawalParams{
		ID:         uuid.New(),
		UserID:     in.UserID,
		ProjectID:  in.ProjectID,
		Amount:     amountNum,
		CardNumber: in.CardNumber,
		CardOwner:  in.CardOwner,
	})
	if err != nil {
		return nil, fmt.Errorf("create withdrawal: %w", err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

func (r *pgRepo) GetWithdrawalByID(ctx context.Context, id uuid.UUID) (*WithdrawalRequest, error) {
	return mapWithdrawal(r.queries.GetPaymentWithdrawalByID(ctx, id))
}

func (r *pgRepo) GetWithdrawalForUser(ctx context.Context, id, userID uuid.UUID) (*WithdrawalRequest, error) {
	return mapWithdrawal(r.queries.GetPaymentWithdrawalForUser(ctx, sqlcdb.GetPaymentWithdrawalForUserParams{
		ID: id, UserID: userID,
	}))
}

func (r *pgRepo) ListWithdrawalsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]WithdrawalRequest, error) {
	rows, err := r.queries.ListPaymentWithdrawalsForUser(ctx, sqlcdb.ListPaymentWithdrawalsForUserParams{
		UserID: userID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list withdrawals: %w", err)
	}
	out := make([]WithdrawalRequest, len(rows))
	for i, row := range rows {
		out[i] = withdrawalFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountWithdrawalsForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := r.queries.CountPaymentWithdrawalsForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count withdrawals: %w", err)
	}
	return n, nil
}

func (r *pgRepo) DeletePendingWithdrawal(ctx context.Context, id, userID uuid.UUID) error {
	rows, err := r.queries.DeletePendingPaymentWithdrawal(ctx, sqlcdb.DeletePendingPaymentWithdrawalParams{
		ID: id, UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("delete pending withdrawal: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) ListWithdrawalsAdmin(ctx context.Context, status string, limit, offset int32) ([]WithdrawalRequest, error) {
	rows, err := r.queries.ListPaymentWithdrawalsAdmin(ctx, sqlcdb.ListPaymentWithdrawalsAdminParams{
		Status: strPtr(status), Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list withdrawals admin: %w", err)
	}
	out := make([]WithdrawalRequest, len(rows))
	for i, row := range rows {
		w := withdrawalFromSQLC(row.PaymentWithdrawalRequest)
		w.UserPhone = deref(row.UserPhone)
		w.ProjectName = deref(row.ProjectName)
		out[i] = w
	}
	return out, nil
}

func (r *pgRepo) CountWithdrawalsAdmin(ctx context.Context, status string) (int64, error) {
	n, err := r.queries.CountPaymentWithdrawalsAdmin(ctx, strPtr(status))
	if err != nil {
		return 0, fmt.Errorf("count withdrawals admin: %w", err)
	}
	return n, nil
}

func (r *pgRepo) GetWithdrawalByIDAdmin(ctx context.Context, id uuid.UUID) (*WithdrawalRequest, error) {
	row, err := r.queries.GetPaymentWithdrawalByIDAdmin(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query withdrawal admin: %w", err)
	}
	w := withdrawalFromSQLC(row.PaymentWithdrawalRequest)
	w.UserPhone = deref(row.UserPhone)
	w.ProjectName = deref(row.ProjectName)
	return &w, nil
}

// ApproveWithdrawal debits users.balance (via applyBalanceDeltaTx, guarded so
// it never goes negative) and marks the request approved in the same
// transaction — mirrors administration/views/bot_withdrawals.py::approve.
func (r *pgRepo) ApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*WithdrawalRequest, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin approve withdrawal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.queries.WithTx(tx)

	wd, err := q.GetPaymentWithdrawalByID(ctx, id)
	if err != nil {
		return nil, mapNoRows(err)
	}
	if wd.Status != WithdrawalStatusPending {
		return nil, ErrWithdrawalNotPending
	}

	amountSom, err := numericToWholeSom(wd.Amount)
	if err != nil {
		return nil, fmt.Errorf("parse withdrawal amount: %w", err)
	}

	if _, _, err := r.applyBalanceDeltaTx(ctx, q, BalanceDeltaInput{
		UserID:         wd.UserID,
		DeltaSom:       -amountSom,
		Comment:        fmt.Sprintf("Pul yechish #%s", id),
		IdempotencyKey: fmt.Sprintf("withdrawal:%s", id),
	}); err != nil {
		return nil, err
	}

	row, err := q.ApprovePaymentWithdrawal(ctx, sqlcdb.ApprovePaymentWithdrawalParams{ID: id, ReviewedBy: &reviewerID})
	if err != nil {
		return nil, mapNoRows(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit approve withdrawal: %w", err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

func (r *pgRepo) RejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*WithdrawalRequest, error) {
	return mapWithdrawal(r.queries.RejectPaymentWithdrawal(ctx, sqlcdb.RejectPaymentWithdrawalParams{
		ID: id, RejectionReason: strPtr(reason), ReviewedBy: &reviewerID,
	}))
}

func (r *pgRepo) SumReservedWithdrawals(ctx context.Context, userID uuid.UUID) (string, error) {
	n, err := r.queries.SumReservedPaymentWithdrawals(ctx, userID)
	if err != nil {
		return "0", fmt.Errorf("sum reserved withdrawals: %w", err)
	}
	return numericToString(n), nil
}

// ===================== cross-cutting: users table =====================

func (r *pgRepo) GetUserBalance(ctx context.Context, userID uuid.UUID) (int64, error) {
	u, err := r.queries.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, database.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("get user balance: %w", err)
	}
	return u.Balance, nil
}

func (r *pgRepo) IsUserKYCVerified(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := r.queries.IsUserKYCVerified(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("check kyc verified: %w", err)
	}
	return ok, nil
}

// ApplyBalanceDelta wraps applyBalanceDeltaTx in its own transaction. See
// domain.go's BalanceDeltaInput doc for the idempotency contract.
func (r *pgRepo) ApplyBalanceDelta(ctx context.Context, in BalanceDeltaInput) (bool, int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("begin apply balance delta: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.queries.WithTx(tx)

	applied, newBalance, err := r.applyBalanceDeltaTx(ctx, q, in)
	if err != nil {
		return false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, fmt.Errorf("commit apply balance delta: %w", err)
	}
	return applied, newBalance, nil
}

// applyBalanceDeltaTx records a History row first (mirrors
// services/balance.py::DjangoBalanceService._apply). ON CONFLICT DO NOTHING
// on idempotency_key makes a retried call return applied=false instead of
// erroring — the safe no-op path a retried webhook/admin click needs.
func (r *pgRepo) applyBalanceDeltaTx(ctx context.Context, q *sqlcdb.Queries, in BalanceDeltaInput) (bool, int64, error) {
	kind := HistoryIncome
	abs := in.DeltaSom
	if abs < 0 {
		kind = HistoryExpense
		abs = -abs
	}
	amountNum, err := stringToNumeric(strconv.FormatInt(abs, 10))
	if err != nil {
		return false, 0, fmt.Errorf("format balance delta amount: %w", err)
	}

	_, err = q.CreatePaymentHistory(ctx, sqlcdb.CreatePaymentHistoryParams{
		ID:             uuid.New(),
		UserID:         &in.UserID,
		Amount:         amountNum,
		Comment:        strPtr(in.Comment),
		Type:           kind,
		IdempotencyKey: strPtr(in.IdempotencyKey),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// idempotency_key already consumed by an earlier call -> no-op.
		u, balErr := q.GetUserByID(ctx, in.UserID)
		if balErr != nil {
			return false, 0, fmt.Errorf("read balance after balance-delta no-op: %w", balErr)
		}
		return false, u.Balance, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("insert balance history: %w", err)
	}

	u, err := q.AddUserBalance(ctx, sqlcdb.AddUserBalanceParams{ID: in.UserID, Delta: in.DeltaSom})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, ErrInsufficientFunds
	}
	if err != nil {
		return false, 0, fmt.Errorf("apply balance delta: %w", err)
	}
	return true, u.Balance, nil
}

// ===================== mapping helpers =====================

func mapTransaction(row sqlcdb.Transaction, err error) (*Transaction, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query payment transaction: %w", err)
	}
	t := transactionFromSQLC(row)
	return &t, nil
}

func transactionFromSQLC(r sqlcdb.Transaction) Transaction {
	return Transaction{
		ID:             r.ID,
		UserID:         r.UserID,
		Amount:         numericToString(r.Amount),
		Status:         r.Status,
		IdempotencyKey: deref(r.IdempotencyKey),
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func mapOrder(row sqlcdb.PaymentOrder, err error) (*Order, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query payment order: %w", err)
	}
	o := orderFromSQLC(row)
	return &o, nil
}

func orderFromSQLC(r sqlcdb.PaymentOrder) Order {
	return Order{
		ID:             r.ID,
		ProjectID:      r.ProjectID,
		OwnerID:        r.OwnerID,
		PayerChatID:    r.PayerChatID,
		PayerFirstName: deref(r.PayerFirstName),
		PayerLastName:  deref(r.PayerLastName),
		PayerUsername:  deref(r.PayerUsername),
		Amount:         numericToString(r.Amount),
		Commission:     numericToString(r.Commission),
		NetAmount:      numericToString(r.NetAmount),
		Provider:       r.Provider,
		Status:         r.Status,
		ExternalID:     deref(r.ExternalID),
		Comment:        deref(r.Comment),
		IdempotencyKey: deref(r.IdempotencyKey),
		TransactionID:  r.TransactionID,
		PaidAt:         r.PaidAt,
		IsDebug:        r.IsDebug,
		PayInstruction: r.PayInstruction,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func mapWithdrawal(row sqlcdb.PaymentWithdrawalRequest, err error) (*WithdrawalRequest, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query withdrawal: %w", err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

func withdrawalFromSQLC(r sqlcdb.PaymentWithdrawalRequest) WithdrawalRequest {
	return WithdrawalRequest{
		ID:              r.ID,
		UserID:          r.UserID,
		ProjectID:       r.ProjectID,
		Amount:          numericToString(r.Amount),
		CardNumber:      r.CardNumber,
		CardOwner:       r.CardOwner,
		Status:          r.Status,
		RejectionReason: deref(r.RejectionReason),
		ReviewedBy:      r.ReviewedBy,
		ReviewedAt:      r.ReviewedAt,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func mapHistory(row sqlcdb.History, err error) (*History, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	h := historyFromSQLC(row)
	return &h, nil
}

func historyFromSQLC(r sqlcdb.History) History {
	return History{
		ID:             r.ID,
		UserID:         r.UserID,
		Amount:         numericToString(r.Amount),
		Comment:        deref(r.Comment),
		Type:           r.Type,
		IdempotencyKey: deref(r.IdempotencyKey),
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func mapNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return database.ErrNotFound
	}
	return fmt.Errorf("query payment: %w", err)
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

// stringToNumeric parses a decimal string ("12.50") into a pgtype.Numeric for
// a money/NUMERIC query param.
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

// numericToWholeSom truncates a NUMERIC(20,2) so'm amount to whole so'm,
// matching Django's `int(Decimal(amount))` used by services/balance.py.
func numericToWholeSom(n pgtype.Numeric) (int64, error) {
	cents, err := parseMoneyCents(numericToString(n))
	if err != nil {
		return 0, err
	}
	return cents / 100, nil
}
