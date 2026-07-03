package payment

import (
	"context"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// ===================== in-memory fake Repository =====================
// No DB required — mirrors user/usecase_test.go's fakeRepo pattern.

type fakeUser struct {
	balance     int64
	kycVerified bool
}

type fakeProject struct {
	ownerID uuid.UUID
	name    string
}

type fakeRepo struct {
	users        map[uuid.UUID]*fakeUser
	projects     map[uuid.UUID]*fakeProject
	transactions map[uuid.UUID]*Transaction
	txnByRef     map[string]uuid.UUID
	orders       map[uuid.UUID]*Order
	orderByIdem  map[string]uuid.UUID
	orderByTxn   map[uuid.UUID]uuid.UUID
	withdrawals  map[uuid.UUID]*WithdrawalRequest
	historyKeys  map[string]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		users:        map[uuid.UUID]*fakeUser{},
		projects:     map[uuid.UUID]*fakeProject{},
		transactions: map[uuid.UUID]*Transaction{},
		txnByRef:     map[string]uuid.UUID{},
		orders:       map[uuid.UUID]*Order{},
		orderByIdem:  map[string]uuid.UUID{},
		orderByTxn:   map[uuid.UUID]uuid.UUID{},
		withdrawals:  map[uuid.UUID]*WithdrawalRequest{},
		historyKeys:  map[string]bool{},
	}
}

// ---- transactions ----

func (r *fakeRepo) CreateTransaction(_ context.Context, in CreateTransactionInput) (*Transaction, error) {
	t := &Transaction{ID: in.ID, UserID: in.UserID, Amount: in.Amount, IdempotencyKey: in.IdempotencyKey, CreatedAt: time.Now()}
	r.transactions[t.ID] = t
	cp := *t
	return &cp, nil
}
func (r *fakeRepo) GetTransactionByID(_ context.Context, id uuid.UUID) (*Transaction, error) {
	t, ok := r.transactions[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *t
	return &cp, nil
}
func (r *fakeRepo) GetTransactionForUser(ctx context.Context, id, userID uuid.UUID) (*Transaction, error) {
	t, err := r.GetTransactionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.UserID != userID {
		return nil, database.ErrNotFound
	}
	return t, nil
}
func (r *fakeRepo) ListTransactionsForUser(_ context.Context, _ uuid.UUID, _, _ int32) ([]Transaction, error) {
	return nil, nil
}
func (r *fakeRepo) CountTransactionsForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *fakeRepo) SetTransactionStatus(_ context.Context, id uuid.UUID, from, to bool) error {
	t, ok := r.transactions[id]
	if !ok || t.Status != from {
		return database.ErrNotFound
	}
	t.Status = to
	return nil
}
func (r *fakeRepo) SetTransactionExternalRef(_ context.Context, id uuid.UUID, ref string) error {
	t, ok := r.transactions[id]
	if !ok || t.IdempotencyKey != "" {
		return database.ErrNotFound
	}
	t.IdempotencyKey = ref
	r.txnByRef[ref] = id
	return nil
}
func (r *fakeRepo) GetTransactionByExternalRef(_ context.Context, ref string) (*Transaction, error) {
	id, ok := r.txnByRef[ref]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *r.transactions[id]
	return &cp, nil
}
func (r *fakeRepo) ListTransactionsByExternalRefDateRange(_ context.Context, from, to time.Time) ([]Transaction, error) {
	var out []Transaction
	for _, t := range r.transactions {
		if t.IdempotencyKey == "" {
			continue
		}
		if t.CreatedAt.Before(from) || t.CreatedAt.After(to) {
			continue
		}
		out = append(out, *t)
	}
	return out, nil
}

// ---- orders ----

func (r *fakeRepo) CreateOrder(_ context.Context, in CreateOrderParams) (*Order, error) {
	o := &Order{
		ID: in.ID, ProjectID: in.ProjectID, OwnerID: in.OwnerID, PayerChatID: in.PayerChatID,
		PayerFirstName: in.PayerFirstName, PayerLastName: in.PayerLastName, PayerUsername: in.PayerUsername,
		Amount: in.Amount, Commission: in.Commission, NetAmount: in.NetAmount, Provider: in.Provider,
		Status: OrderStatusCreated, Comment: in.Comment, IdempotencyKey: in.IdempotencyKey,
		TransactionID: in.TransactionID, IsDebug: in.IsDebug, CreatedAt: time.Now(),
	}
	r.orders[o.ID] = o
	if in.IdempotencyKey != "" {
		r.orderByIdem[in.IdempotencyKey] = o.ID
	}
	if in.TransactionID != nil {
		r.orderByTxn[*in.TransactionID] = o.ID
	}
	cp := *o
	return &cp, nil
}
func (r *fakeRepo) GetOrderByID(_ context.Context, id uuid.UUID) (*Order, error) {
	o, ok := r.orders[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *o
	return &cp, nil
}
func (r *fakeRepo) GetOrderForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Order, error) {
	o, err := r.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	return o, nil
}
func (r *fakeRepo) GetOrderByIdempotencyKey(_ context.Context, key string) (*Order, error) {
	id, ok := r.orderByIdem[key]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *r.orders[id]
	return &cp, nil
}
func (r *fakeRepo) GetOrderByTransactionID(_ context.Context, txnID uuid.UUID) (*Order, error) {
	id, ok := r.orderByTxn[txnID]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *r.orders[id]
	return &cp, nil
}
func (r *fakeRepo) AttachTransactionToOrder(_ context.Context, orderID, txnID uuid.UUID) (*Order, error) {
	o, ok := r.orders[orderID]
	if !ok {
		return nil, database.ErrNotFound
	}
	o.TransactionID = &txnID
	r.orderByTxn[txnID] = orderID
	cp := *o
	return &cp, nil
}
func (r *fakeRepo) ClearOrderIdempotencyKey(_ context.Context, orderID uuid.UUID) error {
	o, ok := r.orders[orderID]
	if !ok {
		return database.ErrNotFound
	}
	if o.IdempotencyKey != "" {
		delete(r.orderByIdem, o.IdempotencyKey)
	}
	o.IdempotencyKey = ""
	return nil
}
func (r *fakeRepo) SetOrderPayInstruction(_ context.Context, orderID uuid.UUID, externalID, payInstruction string) (*Order, error) {
	o, ok := r.orders[orderID]
	if !ok {
		return nil, database.ErrNotFound
	}
	o.ExternalID, o.PayInstruction = externalID, payInstruction
	cp := *o
	return &cp, nil
}
func (r *fakeRepo) MarkOrderPaid(_ context.Context, id uuid.UUID, commission, netAmount, externalID string) (*Order, error) {
	o, ok := r.orders[id]
	if !ok || o.Status != OrderStatusCreated {
		return nil, database.ErrNotFound
	}
	o.Status = OrderStatusPaid
	o.Commission, o.NetAmount, o.ExternalID = commission, netAmount, externalID
	now := time.Now()
	o.PaidAt = &now
	cp := *o
	return &cp, nil
}
func (r *fakeRepo) MarkOrderCancelled(_ context.Context, id uuid.UUID) (*Order, error) {
	o, ok := r.orders[id]
	if !ok || o.Status == OrderStatusCancelled {
		return nil, database.ErrNotFound
	}
	o.Status = OrderStatusCancelled
	cp := *o
	return &cp, nil
}
func (r *fakeRepo) ListPaidOrdersForOwner(_ context.Context, _ EarningsFilter) ([]Order, error) {
	return nil, nil
}
func (r *fakeRepo) CountPaidOrdersForOwner(_ context.Context, _ uuid.UUID, _ *uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *fakeRepo) SumEarningsForOwner(_ context.Context, _ uuid.UUID) (string, string, error) {
	return "0", "0", nil
}
func (r *fakeRepo) EarningsByProject(_ context.Context, _ uuid.UUID) ([]ProjectEarning, error) {
	return nil, nil
}
func (r *fakeRepo) SumBotEarnedForOwner(_ context.Context, _ uuid.UUID) (string, error) {
	return "0", nil
}

func (r *fakeRepo) GetProjectOwner(_ context.Context, projectID uuid.UUID) (uuid.UUID, string, error) {
	p, ok := r.projects[projectID]
	if !ok {
		return uuid.Nil, "", database.ErrNotFound
	}
	return p.ownerID, p.name, nil
}

// ---- history ----

func (r *fakeRepo) ListHistoryForUser(_ context.Context, _ uuid.UUID, _, _ int32) ([]History, error) {
	return nil, nil
}
func (r *fakeRepo) CountHistoryForUser(_ context.Context, _ uuid.UUID) (int64, error) { return 0, nil }
func (r *fakeRepo) GetHistoryForUser(_ context.Context, _, _ uuid.UUID) (*History, error) {
	return nil, database.ErrNotFound
}

// ---- withdrawals ----

func (r *fakeRepo) CreateWithdrawal(_ context.Context, in CreateWithdrawalInput) (*WithdrawalRequest, error) {
	w := &WithdrawalRequest{
		ID: uuid.New(), UserID: in.UserID, ProjectID: in.ProjectID, Amount: in.Amount,
		CardNumber: in.CardNumber, CardOwner: in.CardOwner, Status: WithdrawalStatusPending, CreatedAt: time.Now(),
	}
	r.withdrawals[w.ID] = w
	cp := *w
	return &cp, nil
}
func (r *fakeRepo) GetWithdrawalByID(_ context.Context, id uuid.UUID) (*WithdrawalRequest, error) {
	w, ok := r.withdrawals[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *w
	return &cp, nil
}
func (r *fakeRepo) GetWithdrawalForUser(ctx context.Context, id, userID uuid.UUID) (*WithdrawalRequest, error) {
	w, err := r.GetWithdrawalByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.UserID != userID {
		return nil, database.ErrNotFound
	}
	return w, nil
}
func (r *fakeRepo) ListWithdrawalsForUser(_ context.Context, _ uuid.UUID, _, _ int32) ([]WithdrawalRequest, error) {
	return nil, nil
}
func (r *fakeRepo) CountWithdrawalsForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *fakeRepo) DeletePendingWithdrawal(_ context.Context, id, userID uuid.UUID) error {
	w, ok := r.withdrawals[id]
	if !ok || w.UserID != userID || w.Status != WithdrawalStatusPending {
		return database.ErrNotFound
	}
	delete(r.withdrawals, id)
	return nil
}
func (r *fakeRepo) ListWithdrawalsAdmin(_ context.Context, _ string, _, _ int32) ([]WithdrawalRequest, error) {
	return nil, nil
}
func (r *fakeRepo) CountWithdrawalsAdmin(_ context.Context, _ string) (int64, error) { return 0, nil }
func (r *fakeRepo) GetWithdrawalByIDAdmin(ctx context.Context, id uuid.UUID) (*WithdrawalRequest, error) {
	return r.GetWithdrawalByID(ctx, id)
}
func (r *fakeRepo) ListTransactionsAdmin(_ context.Context, _, _ int32) ([]Transaction, error) {
	return nil, nil
}
func (r *fakeRepo) CountTransactionsAdmin(_ context.Context) (int64, error) { return 0, nil }
func (r *fakeRepo) ListOrdersAdmin(_ context.Context, _ string, _, _ int32) ([]Order, error) {
	return nil, nil
}
func (r *fakeRepo) CountOrdersAdmin(_ context.Context, _ string) (int64, error) { return 0, nil }
func (r *fakeRepo) ApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*WithdrawalRequest, error) {
	w, ok := r.withdrawals[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if w.Status != WithdrawalStatusPending {
		return nil, ErrWithdrawalNotPending
	}
	amountSom, _ := parseMoneySom(w.Amount)
	if _, _, err := r.ApplyBalanceDelta(ctx, BalanceDeltaInput{
		UserID: w.UserID, DeltaSom: -amountSom, IdempotencyKey: "withdrawal:" + id.String(),
	}); err != nil {
		return nil, err
	}
	w.Status = WithdrawalStatusApproved
	w.ReviewedBy = &reviewerID
	cp := *w
	return &cp, nil
}
func (r *fakeRepo) RejectWithdrawal(_ context.Context, id, reviewerID uuid.UUID, reason string) (*WithdrawalRequest, error) {
	w, ok := r.withdrawals[id]
	if !ok || w.Status != WithdrawalStatusPending {
		return nil, database.ErrNotFound
	}
	w.Status = WithdrawalStatusRejected
	w.RejectionReason = reason
	w.ReviewedBy = &reviewerID
	cp := *w
	return &cp, nil
}
func (r *fakeRepo) SumReservedWithdrawals(_ context.Context, _ uuid.UUID) (string, error) {
	return "0", nil
}

// ---- users (balance + KYC) ----

func (r *fakeRepo) GetUserBalance(_ context.Context, userID uuid.UUID) (int64, error) {
	u, ok := r.users[userID]
	if !ok {
		return 0, database.ErrNotFound
	}
	return u.balance, nil
}
func (r *fakeRepo) IsUserKYCVerified(_ context.Context, userID uuid.UUID) (bool, error) {
	u, ok := r.users[userID]
	if !ok {
		return false, nil
	}
	return u.kycVerified, nil
}

// ApplyBalanceDelta emulates the History-first idempotency guard: a repeated
// IdempotencyKey is a no-op, exactly like the real Postgres ON CONFLICT DO
// NOTHING path in repository.go.
func (r *fakeRepo) ApplyBalanceDelta(_ context.Context, in BalanceDeltaInput) (bool, int64, error) {
	u, ok := r.users[in.UserID]
	if !ok {
		u = &fakeUser{}
		r.users[in.UserID] = u
	}
	if in.IdempotencyKey != "" {
		if r.historyKeys[in.IdempotencyKey] {
			return false, u.balance, nil
		}
		r.historyKeys[in.IdempotencyKey] = true
	}
	if u.balance+in.DeltaSom < 0 {
		return false, 0, ErrInsufficientFunds
	}
	u.balance += in.DeltaSom
	return true, u.balance, nil
}

// ===================== tests =====================

func newTestUsecase(repo Repository, revenueUserID *uuid.UUID) *usecase {
	return &usecase{
		repo: repo, notifier: NewNoopNotifier(), commissionPercent: 5, revenueUserID: revenueUserID,
		click: NewClickGatewayFromEnv(), payme: NewPaymeGatewayFromEnv(),
	}
}

// TestSettlePaidTransaction_CommissionSplit verifies the core money-split
// invariant: on a paid webhook, the owner is credited net_amount and the
// platform revenue account is credited commission, out of the same order
// (net_amount + commission == amount).
func TestSettlePaidTransaction_CommissionSplit(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ownerID, revenueID, projectID := uuid.New(), uuid.New(), uuid.New()
	repo.projects[projectID] = &fakeProject{ownerID: ownerID, name: "MyBot"}
	repo.users[ownerID] = &fakeUser{}
	repo.users[revenueID] = &fakeUser{}

	uc := newTestUsecase(repo, &revenueID)

	result, err := uc.CreateOrder(ctx, CreateOrderInput{
		ProjectID: projectID, PayerChatID: 12345, Amount: "10000.00", Provider: ProviderClick,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if result.Order.Commission != "500.00" || result.Order.NetAmount != "9500.00" {
		t.Fatalf("unexpected commission split: commission=%s net=%s", result.Order.Commission, result.Order.NetAmount)
	}
	if result.Order.TransactionID == nil {
		t.Fatal("expected a transaction to be linked to the new order")
	}

	updated, err := uc.SettlePaidTransaction(ctx, *result.Order.TransactionID, "click-123")
	if err != nil {
		t.Fatalf("settle paid: %v", err)
	}
	if updated.Status != OrderStatusPaid {
		t.Fatalf("expected order status paid, got %s", updated.Status)
	}
	if repo.users[ownerID].balance != 9500 {
		t.Fatalf("owner not credited net_amount: got balance=%d, want 9500", repo.users[ownerID].balance)
	}
	if repo.users[revenueID].balance != 500 {
		t.Fatalf("platform not credited commission: got balance=%d, want 500", repo.users[revenueID].balance)
	}
}

// TestSettlePaidTransaction_Idempotent verifies a retried webhook call (Click
// resending Complete, Payme resending PerformTransaction) never double-credits
// the owner — both the status guard (order already 'paid') and the
// History-idempotency-key guard inside ApplyBalanceDelta protect this.
func TestSettlePaidTransaction_Idempotent(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ownerID, projectID := uuid.New(), uuid.New()
	repo.projects[projectID] = &fakeProject{ownerID: ownerID, name: "MyBot"}
	repo.users[ownerID] = &fakeUser{}

	uc := newTestUsecase(repo, nil)

	result, err := uc.CreateOrder(ctx, CreateOrderInput{
		ProjectID: projectID, PayerChatID: 1, Amount: "10000.00", Provider: ProviderClick,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	txnID := *result.Order.TransactionID

	if _, err := uc.SettlePaidTransaction(ctx, txnID, "ext-1"); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	if got := repo.users[ownerID].balance; got != 9500 {
		t.Fatalf("balance after first settle = %d, want 9500", got)
	}

	if _, err := uc.SettlePaidTransaction(ctx, txnID, "ext-1"); err != nil {
		t.Fatalf("second (retried) settle: %v", err)
	}
	if got := repo.users[ownerID].balance; got != 9500 {
		t.Fatalf("balance double-credited on retry: got %d, want 9500", got)
	}
}

// TestCreateWithdrawal_RequiresKYC mirrors serializers/withdrawal.py's KYC
// gate: an unverified user cannot request a payout.
func TestCreateWithdrawal_RequiresKYC(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	userID := uuid.New()
	repo.users[userID] = &fakeUser{kycVerified: false}
	uc := newTestUsecase(repo, nil)

	_, err := uc.CreateWithdrawal(ctx, CreateWithdrawalInput{
		UserID: userID, Amount: "5000.00", CardNumber: "8600123456781234", CardOwner: "A B",
	})
	ae, ok := httpx.AsAppError(err)
	if !ok || ae.Status != 403 {
		t.Fatalf("expected 403 kyc_required error, got %v", err)
	}
}
