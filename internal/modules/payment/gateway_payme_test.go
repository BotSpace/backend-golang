package payment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func testPaymeGateway() PaymeGateway {
	return PaymeGateway{ID: "merchant-1", Key: "test-payme-key", ReturnURL: "https://botspace.uz"}
}

func newPaymeTestHandler(uc Usecase, g PaymeGateway) *Handler {
	return &Handler{uc: uc, click: NewClickGatewayFromEnv(), payme: g}
}

func paymeBasicAuth(g PaymeGateway) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("Paycom:"+g.Key))
}

func postPaymeWebhook(h *Handler, g PaymeGateway, authHeader, method string, params any) paymeRPCResponse {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	paramsJSON, _ := json.Marshal(params)
	body, _ := json.Marshal(map[string]json.RawMessage{
		"method": mustJSON(method),
		"params": paramsJSON,
		"id":     mustJSON(1),
	})
	req := httptest.NewRequest(http.MethodPost, "/payment/webhooks/payme", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	c.Request = req

	h.PaymeWebhook(c)

	var resp paymeRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		panic(err)
	}
	return resp
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestPaymeWebhook_AuthCheckFailed: a missing/wrong Basic-auth header must be
// rejected before any method dispatch, mirroring payme/views/base.py's
// check_authorize running first in the request pipeline.
func TestPaymeWebhook_AuthCheckFailed(t *testing.T) {
	uc := newTestUsecase(newFakeRepo(), nil)
	h := newPaymeTestHandler(uc, testPaymeGateway())

	resp := postPaymeWebhook(h, testPaymeGateway(), "Basic bm90LXRoZS1yaWdodC1rZXk=", "CheckPerformTransaction", paymeParams{
		Account: paymeAccount{OrderID: uuid.New().String()}, Amount: 100,
	})
	if resp.Error == nil || resp.Error.Code != paymePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %+v", resp.Error)
	}
}

// TestPaymeWebhook_UnknownTransaction: CheckPerformTransaction against an
// order_id we've never heard of must report InvalidAccount, not panic or
// silently allow the transaction.
func TestPaymeWebhook_UnknownTransaction(t *testing.T) {
	g := testPaymeGateway()
	uc := newTestUsecase(newFakeRepo(), nil)
	h := newPaymeTestHandler(uc, g)

	resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "CheckPerformTransaction", paymeParams{
		Account: paymeAccount{OrderID: uuid.New().String()}, Amount: 1000000,
	})
	if resp.Error == nil || resp.Error.Code != paymeInvalidAccount {
		t.Fatalf("expected InvalidAccount, got %+v", resp.Error)
	}
}

// TestPaymeWebhook_IncorrectAmount: CheckPerformTransaction/CreateTransaction
// must reject an amount that doesn't match the transaction (tiyin units).
func TestPaymeWebhook_IncorrectAmount(t *testing.T) {
	repo := newFakeRepo()
	ownerID, projectID := uuid.New(), uuid.New()
	repo.projects[projectID] = &fakeProject{ownerID: ownerID}
	repo.users[ownerID] = &fakeUser{}
	uc := newTestUsecase(repo, nil)

	result, err := uc.CreateOrder(context.Background(), CreateOrderInput{
		ProjectID: projectID, PayerChatID: 1, Amount: "10000.00", Provider: ProviderPayme,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	txnID := *result.Order.TransactionID

	g := testPaymeGateway()
	h := newPaymeTestHandler(uc, g)

	// 10000.00 so'm == 1,000,000 tiyin; claim 1 tiyin instead.
	resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "CheckPerformTransaction", paymeParams{
		Account: paymeAccount{OrderID: txnID.String()}, Amount: 1,
	})
	if resp.Error == nil || resp.Error.Code != paymeIncorrectAmount {
		t.Fatalf("expected IncorrectAmount, got %+v", resp.Error)
	}
}

// TestPaymeWebhook_HappyPath drives CheckPerformTransaction -> CreateTransaction
// -> PerformTransaction -> CheckTransaction, and confirms the owner is
// credited exactly once even if PerformTransaction is retried (Payme resends
// on any ambiguous response, same as Click).
func TestPaymeWebhook_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	ownerID, projectID := uuid.New(), uuid.New()
	repo.projects[projectID] = &fakeProject{ownerID: ownerID}
	repo.users[ownerID] = &fakeUser{}
	uc := newTestUsecase(repo, nil) // commissionPercent: 5

	result, err := uc.CreateOrder(context.Background(), CreateOrderInput{
		ProjectID: projectID, PayerChatID: 1, Amount: "10000.00", Provider: ProviderPayme,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	txnID := *result.Order.TransactionID
	const amountTiyin = 1_000_000 // 10000.00 so'm

	g := testPaymeGateway()
	h := newPaymeTestHandler(uc, g)

	if resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "CheckPerformTransaction", paymeParams{
		Account: paymeAccount{OrderID: txnID.String()}, Amount: amountTiyin,
	}); resp.Error != nil {
		t.Fatalf("CheckPerformTransaction failed: %+v", resp.Error)
	}

	paymeTxnID := "payme-ext-1"
	if resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "CreateTransaction", paymeParams{
		ID: paymeTxnID, Account: paymeAccount{OrderID: txnID.String()}, Amount: amountTiyin,
	}); resp.Error != nil {
		t.Fatalf("CreateTransaction failed: %+v", resp.Error)
	}

	if resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "PerformTransaction", paymeParams{
		ID: paymeTxnID,
	}); resp.Error != nil {
		t.Fatalf("PerformTransaction failed: %+v", resp.Error)
	}
	if got := repo.users[ownerID].balance; got != 9500 {
		t.Fatalf("owner not credited: balance=%d, want 9500", got)
	}

	// Retried PerformTransaction must not double-credit.
	if resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "PerformTransaction", paymeParams{
		ID: paymeTxnID,
	}); resp.Error != nil {
		t.Fatalf("retried PerformTransaction failed: %+v", resp.Error)
	}
	if got := repo.users[ownerID].balance; got != 9500 {
		t.Fatalf("owner double-credited on retry: balance=%d, want 9500", got)
	}

	if resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "CheckTransaction", paymeParams{
		ID: paymeTxnID,
	}); resp.Error != nil {
		t.Fatalf("CheckTransaction failed: %+v", resp.Error)
	} else if state, _ := resp.Result.(map[string]any)["state"].(float64); state != 2 {
		t.Fatalf("expected state=2 (performed), got %v", resp.Result)
	}

	// GetStatement must report the now-performed transaction back within a
	// window covering "now".
	stmt := map[string]any{"from": 0, "to": time.Now().Add(time.Hour).UnixMilli()}
	if resp := postPaymeWebhook(h, g, paymeBasicAuth(g), "GetStatement", stmt); resp.Error != nil {
		t.Fatalf("GetStatement failed: %+v", resp.Error)
	} else {
		txns, _ := resp.Result.(map[string]any)["transactions"].([]any)
		if len(txns) != 1 {
			t.Fatalf("expected 1 transaction in statement, got %d (%v)", len(txns), resp.Result)
		}
	}
}
