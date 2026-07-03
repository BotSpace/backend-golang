package payment

import (
	"context"
	"crypto/md5" //nolint:gosec // matching Click's own signing scheme in the test, not a security choice.
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func testClickGateway() ClickGateway {
	return ClickGateway{ServiceID: "12345", MerchantID: "999", SecretKey: "test-secret", ReturnURL: "https://botspace.uz"}
}

// clickSign computes the exact same MD5 Click's Merchant API sends, so tests
// exercise the real verifySignature code path instead of bypassing it.
func clickSign(g ClickGateway, clickTransID, merchantTransID, prepareID, amount, action, signTime string) string {
	text := clickTransID + g.ServiceID + g.SecretKey + merchantTransID + prepareID + amount + action + signTime
	sum := md5.Sum([]byte(text)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

func newClickTestHandler(uc Usecase, g ClickGateway) *Handler {
	return &Handler{uc: uc, click: g, payme: NewPaymeGatewayFromEnv()}
}

func postClickWebhook(h *Handler, form url.Values) clickResponse {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/payment/webhooks/click", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req

	h.ClickWebhook(c)

	var resp clickResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		panic(err) // test setup bug, not a case under test
	}
	return resp
}

// TestClickWebhook_SignCheckFailed: a bad sign_string must never reach
// transaction lookup/settlement.
func TestClickWebhook_SignCheckFailed(t *testing.T) {
	uc := newTestUsecase(newFakeRepo(), nil)
	g := testClickGateway()
	h := newClickTestHandler(uc, g)

	form := url.Values{}
	form.Set("click_trans_id", "1")
	form.Set("service_id", g.ServiceID)
	form.Set("merchant_trans_id", uuid.New().String())
	form.Set("amount", "100.00")
	form.Set("action", "0")
	form.Set("sign_time", "2024-01-01 00:00:00")
	form.Set("sign_string", "not-the-real-signature")

	resp := postClickWebhook(h, form)
	if resp.Error != clickErrSignFailed {
		t.Fatalf("expected SIGN CHECK FAILED (-1), got %d %s", resp.Error, resp.ErrorNote)
	}
}

// TestClickWebhook_UnknownTransaction: a syntactically valid, correctly
// signed call for a transaction id we've never heard of must 404, not panic
// or silently succeed.
func TestClickWebhook_UnknownTransaction(t *testing.T) {
	uc := newTestUsecase(newFakeRepo(), nil)
	g := testClickGateway()
	h := newClickTestHandler(uc, g)

	signTime := "2024-01-01 00:00:00"
	missingID := uuid.New().String()
	sign := clickSign(g, "1", missingID, "", "100.00", "0", signTime)
	form := url.Values{}
	form.Set("click_trans_id", "1")
	form.Set("service_id", g.ServiceID)
	form.Set("merchant_trans_id", missingID)
	form.Set("amount", "100.00")
	form.Set("action", "0")
	form.Set("sign_time", signTime)
	form.Set("sign_string", sign)

	resp := postClickWebhook(h, form)
	if resp.Error != clickErrAccountNotFound {
		t.Fatalf("expected AccountNotFound (-5), got %d %s", resp.Error, resp.ErrorNote)
	}
}

// TestClickWebhook_IncorrectAmount ports click_up's check_amount: Prepare (or
// Complete) claiming a different amount than the transaction actually is for
// must be rejected with IncorrectAmount, never silently accepted — this was
// a real gap (no amount check existed at all before this fix).
func TestClickWebhook_IncorrectAmount(t *testing.T) {
	repo := newFakeRepo()
	ownerID, projectID := uuid.New(), uuid.New()
	repo.projects[projectID] = &fakeProject{ownerID: ownerID}
	repo.users[ownerID] = &fakeUser{}
	uc := newTestUsecase(repo, nil)

	result, err := uc.CreateOrder(context.Background(), CreateOrderInput{
		ProjectID: projectID, PayerChatID: 1, Amount: "10000.00", Provider: ProviderClick,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	txnID := *result.Order.TransactionID

	g := testClickGateway()
	h := newClickTestHandler(uc, g)
	signTime := "2024-01-01 00:00:00"

	// Transaction is for 10000.00; webhook claims 1.00 — correctly signed for
	// that (wrong) amount, so only the amount check can catch this.
	sign := clickSign(g, "1", txnID.String(), "", "1.00", "0", signTime)
	form := url.Values{}
	form.Set("click_trans_id", "1")
	form.Set("service_id", g.ServiceID)
	form.Set("merchant_trans_id", txnID.String())
	form.Set("amount", "1.00")
	form.Set("action", "0")
	form.Set("sign_time", signTime)
	form.Set("sign_string", sign)

	resp := postClickWebhook(h, form)
	if resp.Error != clickErrIncorrectAmount {
		t.Fatalf("expected IncorrectAmount (-2), got %d %s", resp.Error, resp.ErrorNote)
	}
}

// TestClickWebhook_HappyPath_PrepareThenComplete drives the full Prepare ->
// Complete flow with correctly signed, correctly amounted requests and
// checks the owner is credited exactly once even if Click retries Complete
// (which real merchants do on any ambiguous response).
func TestClickWebhook_HappyPath_PrepareThenComplete(t *testing.T) {
	repo := newFakeRepo()
	ownerID, projectID := uuid.New(), uuid.New()
	repo.projects[projectID] = &fakeProject{ownerID: ownerID}
	repo.users[ownerID] = &fakeUser{}
	uc := newTestUsecase(repo, nil) // commissionPercent: 5 (see newTestUsecase)

	result, err := uc.CreateOrder(context.Background(), CreateOrderInput{
		ProjectID: projectID, PayerChatID: 1, Amount: "10000.00", Provider: ProviderClick,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	txnID := *result.Order.TransactionID

	g := testClickGateway()
	h := newClickTestHandler(uc, g)
	signTime := "2024-01-01 00:00:00"

	prepareForm := url.Values{}
	prepareForm.Set("click_trans_id", "1")
	prepareForm.Set("service_id", g.ServiceID)
	prepareForm.Set("merchant_trans_id", txnID.String())
	prepareForm.Set("amount", "10000.00")
	prepareForm.Set("action", "0")
	prepareForm.Set("sign_time", signTime)
	prepareForm.Set("sign_string", clickSign(g, "1", txnID.String(), "", "10000.00", "0", signTime))
	if resp := postClickWebhook(h, prepareForm); resp.Error != clickErrOK {
		t.Fatalf("prepare failed: %d %s", resp.Error, resp.ErrorNote)
	}

	completeForm := url.Values{}
	completeForm.Set("click_trans_id", "1")
	completeForm.Set("service_id", g.ServiceID)
	completeForm.Set("merchant_trans_id", txnID.String())
	completeForm.Set("amount", "10000.00")
	completeForm.Set("action", "1")
	completeForm.Set("error", "0")
	completeForm.Set("sign_time", signTime)
	completeForm.Set("sign_string", clickSign(g, "1", txnID.String(), "", "10000.00", "1", signTime))

	if resp := postClickWebhook(h, completeForm); resp.Error != clickErrOK {
		t.Fatalf("complete failed: %d %s", resp.Error, resp.ErrorNote)
	}
	if got := repo.users[ownerID].balance; got != 9500 {
		t.Fatalf("owner not credited: balance=%d, want 9500", got)
	}

	// Click retries Complete (real-world behavior on any timeout/ambiguity) —
	// must not double-credit.
	if resp := postClickWebhook(h, completeForm); resp.Error != clickErrOK {
		t.Fatalf("retried complete failed: %d %s", resp.Error, resp.ErrorNote)
	}
	if got := repo.users[ownerID].balance; got != 9500 {
		t.Fatalf("owner double-credited on retry: balance=%d, want 9500", got)
	}
}
