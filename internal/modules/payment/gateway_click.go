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
	ClickTransID      string `json:"click_trans_id"`
	MerchantTransID   string `json:"merchant_trans_id"`
	MerchantPrepareID string `json:"merchant_prepare_id,omitempty"`
	MerchantConfirmID string `json:"merchant_confirm_id,omitempty"`
	Error             int    `json:"error"`
	ErrorNote         string `json:"error_note"`
}

// Click error codes we emit (subset of the Merchant API spec).
const (
	clickErrOK              = 0
	clickErrSignFailed      = -1
	clickErrActionNotFound  = -3
	clickErrAccountNotFound = -5
	clickErrBadRequest      = -8
	clickErrTransCancelled  = -9
)

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
		c.JSON(200, clickResponse{
			ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
			Error: clickErrSignFailed, ErrorNote: "SIGN CHECK FAILED",
		})
		return
	}

	txnID, err := uuid.Parse(p.MerchantTransID)
	if err != nil {
		c.JSON(200, clickResponse{
			ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
			Error: clickErrBadRequest, ErrorNote: "Error in request from click",
		})
		return
	}
	txn, err := h.uc.GetTransactionByID(c.Request.Context(), txnID)
	if err != nil {
		c.JSON(200, clickResponse{
			ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
			Error: clickErrAccountNotFound, ErrorNote: "User does not exist",
		})
		return
	}

	switch p.Action {
	case "0": // Prepare
		c.JSON(200, clickResponse{
			ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
			MerchantPrepareID: txn.ID.String(), Error: clickErrOK, ErrorNote: "success",
		})
	case "1": // Complete
		h.clickComplete(c, p, txn.ID)
	default:
		c.JSON(200, clickResponse{
			ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
			Error: clickErrActionNotFound, ErrorNote: "Action not found",
		})
	}
}

func (h *Handler) clickComplete(c *gin.Context, p clickParams, txnID uuid.UUID) {
	errCode, _ := strconv.Atoi(p.Error)

	if errCode < 0 {
		if _, err := h.uc.SettleCancelledTransaction(c.Request.Context(), txnID); err != nil {
			c.JSON(200, clickResponse{
				ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
				Error: clickErrTransCancelled, ErrorNote: "Transaction cancelled",
			})
			return
		}
		c.JSON(200, clickResponse{
			ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
			MerchantPrepareID: txnID.String(), MerchantConfirmID: txnID.String(),
			Error: errCode, ErrorNote: p.ErrorNote,
		})
		return
	}

	if _, err := h.uc.SettlePaidTransaction(c.Request.Context(), txnID, p.ClickTransID); err != nil {
		c.JSON(200, clickResponse{
			ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
			Error: clickErrBadRequest, ErrorNote: "Unable to complete the transaction",
		})
		return
	}
	c.JSON(200, clickResponse{
		ClickTransID: p.ClickTransID, MerchantTransID: p.MerchantTransID,
		MerchantPrepareID: txnID.String(), MerchantConfirmID: txnID.String(),
		Error: clickErrOK, ErrorNote: "success",
	})
}

// envOrDefault reads an env var, falling back to def when unset/empty.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
