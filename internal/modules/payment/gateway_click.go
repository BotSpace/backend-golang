package payment

import (
	"crypto/md5" //nolint:gosec // Click's Merchant API mandates MD5 for sign_string; not a security choice we control.
	"encoding/hex"
	"net/url"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ClickGateway generates Click pay URLs and verifies Click Merchant API
// webhook signatures. Ports core/apps/payment/views/click.py plus the
// click_up library it wraps (click_up/views.py::ClickWebhook.check_auth,
// click_up/classes/initializer.py::generate_pay_link).
type ClickGateway struct {
	ServiceID  string
	MerchantID string
	SecretKey  string
	ReturnURL  string
}

// NewClickGatewayFromEnv reads Click credentials from the environment (see
// the task's KEY RULES): APP_CLICK_SERVICE_ID, APP_CLICK_MERCHANT_ID,
// APP_CLICK_SECRET_KEY.
func NewClickGatewayFromEnv() ClickGateway {
	return ClickGateway{
		ServiceID:  os.Getenv("APP_CLICK_SERVICE_ID"),
		MerchantID: os.Getenv("APP_CLICK_MERCHANT_ID"),
		SecretKey:  os.Getenv("APP_CLICK_SECRET_KEY"),
		ReturnURL:  envOrDefault("APP_PAYMENT_RETURN_URL", "https://botspace.uz"),
	}
}

// PayURL builds a my.click.uz checkout link. transactionID is sent as
// transaction_param; Click echoes it back as merchant_trans_id on every
// webhook call, so ClickWebhook below uses it to resolve our transaction row.
func (g ClickGateway) PayURL(transactionID uuid.UUID, amount string) string {
	q := url.Values{}
	q.Set("service_id", g.ServiceID)
	q.Set("merchant_id", g.MerchantID)
	q.Set("amount", amount)
	q.Set("transaction_param", transactionID.String())
	q.Set("return_url", g.ReturnURL)
	return "https://my.click.uz/services/pay?" + q.Encode()
}

// clickParams is the Click Merchant API's incoming POST form, ported field
// for field from click_up/typing/request/shop_api.py::ClickShopApiRequest.
type clickParams struct {
	ClickTransID      string
	ServiceID         string
	MerchantTransID   string
	MerchantPrepareID string
	Amount            string
	Action            string
	Error             string
	ErrorNote         string
	SignTime          string
	SignString        string
}

func parseClickForm(c *gin.Context) clickParams {
	return clickParams{
		ClickTransID:      c.PostForm("click_trans_id"),
		ServiceID:         c.PostForm("service_id"),
		MerchantTransID:   c.PostForm("merchant_trans_id"),
		MerchantPrepareID: c.PostForm("merchant_prepare_id"),
		Amount:            c.PostForm("amount"),
		Action:            c.PostForm("action"),
		Error:             c.PostForm("error"),
		ErrorNote:         c.PostForm("error_note"),
		SignTime:          c.PostForm("sign_time"),
		SignString:        c.PostForm("sign_string"),
	}
}

// verifySignature ports click_up/views.py::ClickWebhook.check_auth: MD5 of
// click_trans_id+service_id+secret_key+merchant_trans_id+merchant_prepare_id+amount+action+sign_time.
func (g ClickGateway) verifySignature(p clickParams) bool {
	if g.ServiceID == "" || g.SecretKey == "" {
		return false
	}
	text := p.ClickTransID + g.ServiceID + g.SecretKey + p.MerchantTransID + p.MerchantPrepareID + p.Amount + p.Action + p.SignTime
	sum := md5.Sum([]byte(text)) //nolint:gosec
	return hex.EncodeToString(sum[:]) == p.SignString
}

// clickResponse is the Click Merchant API's expected JSON reply shape.
type clickResponse struct {
	ClickTransID      int64  `json:"click_trans_id"`
	MerchantTransID   string `json:"merchant_trans_id"`
	MerchantPrepareID *int64 `json:"merchant_prepare_id,omitempty"`
	MerchantConfirmID *int64 `json:"merchant_confirm_id,omitempty"`
	Error             int    `json:"error"`
	ErrorNote         string `json:"error_note"`
}

// Click error codes we emit (subset of the Merchant API spec, click_up/exceptions).
const (
	clickErrOK              = 0
	clickErrSignFailed      = -1
	clickErrIncorrectAmount = -2
	clickErrActionNotFound  = -3
	clickErrAccountNotFound = -5
	clickErrBadRequest      = -8
	clickErrTransCancelled  = -9
)

// amountsMatch ports click_up's check_amount (click_up/views.py): the
// commission percent is an opt-in setting Django leaves unset in this
// deployment (config/conf/click.py: CLICK_COMMISSION_PERCENT commented
// out), so expectedAmount is compared to receivedAmount as-is, tolerant of
// float rounding up to 1 tiyin (0.01) either way — same tolerance Django
// uses (`abs(received_amount - expected_amount) > 0.01`).
func amountsMatch(expectedAmount, receivedAmount string) bool {
	want, err := strconv.ParseFloat(expectedAmount, 64)
	if err != nil {
		return false
	}
	got, err := strconv.ParseFloat(receivedAmount, 64)
	if err != nil {
		return false
	}
	diff := want - got
	if diff < 0 {
		diff = -diff
	}
	return diff <= 0.01
}

// ClickWebhook handles Click's Prepare (action=0) and Complete (action=1)
// calls. This is a deliberately simplified two-phase implementation: we do
// not keep a separate Click-side "prepared" ledger row (out of this port's
// 4-table scope) — Prepare always acknowledges using the transaction id as
// merchant_prepare_id, and the actual settlement happens on Complete, guarded
// by payment_order.status='created'/transaction.status so a retried call
// (or a Prepare without a following Complete) can never double-credit.
//
// Ports core/apps/payment/views/click.py::ClickWebhookAPIView.
func (h *Handler) ClickWebhook(c *gin.Context) {
	p := parseClickForm(c)

	if !h.click.verifySignature(p) {
		c.JSON(200, newClickResponse(p, clickErrSignFailed, "SIGN CHECK FAILED"))
		return
	}

	txnID, err := uuid.Parse(p.MerchantTransID)
	if err != nil {
		c.JSON(200, newClickResponse(p, clickErrBadRequest, "Error in request from click"))
		return
	}
	txn, err := h.uc.GetTransactionByID(c.Request.Context(), txnID)
	if err != nil {
		c.JSON(200, newClickResponse(p, clickErrAccountNotFound, "User does not exist"))
		return
	}

	// Ports click_up's check_perform_transaction, which validates the amount
	// on every call (Prepare AND Complete), before dispatching on action —
	// a mismatched amount must never silently settle the transaction.
	if !amountsMatch(txn.Amount, p.Amount) {
		c.JSON(200, newClickResponse(p, clickErrIncorrectAmount, "Incorrect parameter amount"))
		return
	}

	switch p.Action {
	case "0": // Prepare
		resp := newClickResponse(p, clickErrOK, "success")
		resp.MerchantPrepareID = int64Ptr(clickTransID(p))
		c.JSON(200, resp)
	case "1": // Complete
		h.clickComplete(c, p, txn.ID)
	default:
		c.JSON(200, newClickResponse(p, clickErrActionNotFound, "Action not found"))
	}
}

func (h *Handler) clickComplete(c *gin.Context, p clickParams, txnID uuid.UUID) {
	errCode, _ := strconv.Atoi(p.Error)

	if errCode < 0 {
		if _, err := h.uc.SettleCancelledTransaction(c.Request.Context(), txnID); err != nil {
			c.JSON(200, newClickResponse(p, clickErrTransCancelled, "Transaction cancelled"))
			return
		}
		resp := newClickResponse(p, errCode, p.ErrorNote)
		resp.MerchantPrepareID = int64Ptr(clickTransID(p))
		resp.MerchantConfirmID = int64Ptr(clickTransID(p))
		c.JSON(200, resp)
		return
	}

	if _, err := h.uc.SettlePaidTransaction(c.Request.Context(), txnID, p.ClickTransID); err != nil {
		c.JSON(200, newClickResponse(p, clickErrBadRequest, "Unable to complete the transaction"))
		return
	}
	resp := newClickResponse(p, clickErrOK, "success")
	resp.MerchantPrepareID = int64Ptr(clickTransID(p))
	resp.MerchantConfirmID = int64Ptr(clickTransID(p))
	c.JSON(200, resp)
}

func newClickResponse(p clickParams, errCode int, note string) clickResponse {
	return clickResponse{
		ClickTransID:    clickTransID(p),
		MerchantTransID: p.MerchantTransID,
		Error:           errCode,
		ErrorNote:       note,
	}
}

func clickTransID(p clickParams) int64 {
	id, _ := strconv.ParseInt(p.ClickTransID, 10, 64)
	return id
}

func int64Ptr(v int64) *int64 {
	return &v
}

// envOrDefault reads an env var, falling back to def when unset/empty.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
