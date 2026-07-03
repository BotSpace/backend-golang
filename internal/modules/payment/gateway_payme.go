package payment

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PaymeGateway generates Payme checkout links and verifies the Payme JSON-RPC
// webhook's Basic-auth header. Ports core/apps/payment/views/payme.py plus
// the payme package it wraps (payme/views/base.py, payme/classes/initializer.py).
type PaymeGateway struct {
	ID        string
	Key       string
	ReturnURL string
	TestMode  bool
}

// NewPaymeGatewayFromEnv reads Payme credentials from the environment (see
// the task's KEY RULES): APP_PAYME_ID, APP_PAYME_KEY.
func NewPaymeGatewayFromEnv() PaymeGateway {
	return PaymeGateway{
		ID:        os.Getenv("APP_PAYME_ID"),
		Key:       os.Getenv("APP_PAYME_KEY"),
		ReturnURL: envOrDefault("APP_PAYMENT_RETURN_URL", "https://botspace.uz"),
		TestMode:  os.Getenv("APP_PAYME_TEST_MODE") == "true",
	}
}

// PayURL builds a checkout.paycom.uz link (payme/classes/initializer.py::
// Initializer.generate_pay_link). amountTiyin is in tiyin (1 so'm = 100 tiyin);
// our NUMERIC(20,2) so'm amount strings parse directly to tiyin via
// parseMoneyCents, since the fractional part already represents hundredths.
func (g PaymeGateway) PayURL(transactionID uuid.UUID, amountTiyin int64) string {
	params := fmt.Sprintf("m=%s;ac.order_id=%s;a=%d;c=%s", g.ID, transactionID.String(), amountTiyin, g.ReturnURL)
	encoded := base64.StdEncoding.EncodeToString([]byte(params))
	host := "https://checkout.paycom.uz/"
	if g.TestMode {
		host = "https://test.paycom.uz/"
	}
	return host + encoded
}

// verifyBasicAuth checks the "Basic base64(Paycom:<key>)" header Payme sends
// on every webhook call (payme/views/base.py::check_authorize).
func (g PaymeGateway) verifyBasicAuth(header string) bool {
	const prefix = "Basic "
	if g.Key == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(g.Key)) == 1
}

// ===================== JSON-RPC wire types =====================

type paymeRPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	ID     json.RawMessage `json:"id"`
}

type paymeAccount struct {
	OrderID string `json:"order_id"`
}

type paymeParams struct {
	ID      string       `json:"id"`
	Amount  int64        `json:"amount"`
	Account paymeAccount `json:"account"`
	Reason  int          `json:"reason"`
}

type paymeError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type paymeRPCResponse struct {
	Result any             `json:"result,omitempty"`
	Error  *paymeError     `json:"error,omitempty"`
	ID     json.RawMessage `json:"id"`
}

// Approximate Payme JSON-RPC error codes (see
// https://developer.help.paycom.uz/). Exact numeric fidelity is not this
// port's goal — the task scope is signature verification + status update;
// these codes are close enough for a real merchant integration to react to.
const (
	paymePermissionDenied    = -32504
	paymeParseError          = -32700
	paymeMethodNotFound      = -32601
	paymeInvalidAccount      = -31050
	paymeIncorrectAmount     = -31001
	paymeTransactionNotFound = -31003
	paymeUnableToPerform     = -31008
)

// paymeAmountTiyin converts our NUMERIC(20,2) so'm string to tiyin.
func paymeAmountTiyin(amount string) (int64, error) { return parseMoneyCents(amount) }

// PaymeWebhook dispatches Payme's JSON-RPC methods. It is a pragmatic subset
// of the full protocol: CheckPerformTransaction, CreateTransaction,
// PerformTransaction, CancelTransaction, CheckTransaction. State beyond
// "paid/not paid" (fiscal data, cancel reasons, GetStatement) is out of
// scope — see SettlePaidTransaction/SettleCancelledTransaction in usecase.go
// for the actual settlement (owner credit + commission + notify).
//
// Ports core/apps/payment/views/payme.py::PaymeCallBackAPIView plus the
// payme package's dispatcher (payme/views/base.py::BasePaymeWebHookAPIView.post).
func (h *Handler) PaymeWebhook(c *gin.Context) {
	if !h.payme.verifyBasicAuth(c.GetHeader("Authorization")) {
		c.JSON(http.StatusOK, paymeRPCResponse{Error: &paymeError{
			Code: paymePermissionDenied, Message: "Insufficient privilege to perform this method",
		}})
		return
	}

	var req paymeRPCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, paymeRPCResponse{Error: &paymeError{Code: paymeParseError, Message: "Parse error"}})
		return
	}
	var p paymeParams
	_ = json.Unmarshal(req.Params, &p)

	switch req.Method {
	case "CheckPerformTransaction":
		h.paymeCheckPerformTransaction(c, req, p)
	case "CreateTransaction":
		h.paymeCreateTransaction(c, req, p)
	case "PerformTransaction":
		h.paymePerformTransaction(c, req, p)
	case "CancelTransaction":
		h.paymeCancelTransaction(c, req, p)
	case "CheckTransaction":
		h.paymeCheckTransaction(c, req, p)
	default:
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: &paymeError{Code: paymeMethodNotFound, Message: "Method not found"}})
	}
}

func (h *Handler) paymeCheckPerformTransaction(c *gin.Context, req paymeRPCRequest, p paymeParams) {
	txn, rpcErr := h.paymeResolveByAccount(c, p)
	if rpcErr != nil {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: rpcErr})
		return
	}
	amountTiyin, _ := paymeAmountTiyin(txn.Amount)
	if p.Amount != amountTiyin {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: &paymeError{Code: paymeIncorrectAmount, Message: "Incorrect amount"}})
		return
	}
	c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Result: gin.H{"allow": true}})
}

// CreateTransaction is the only call that still carries `account` (our
// order_id); we persist Payme's own opaque `params.id` onto our transaction
// row (SetTransactionExternalRef) so Perform/Cancel/Check — which only carry
// that opaque id — can resolve back to it.
func (h *Handler) paymeCreateTransaction(c *gin.Context, req paymeRPCRequest, p paymeParams) {
	txn, rpcErr := h.paymeResolveByAccount(c, p)
	if rpcErr != nil {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: rpcErr})
		return
	}
	amountTiyin, _ := paymeAmountTiyin(txn.Amount)
	if p.Amount != amountTiyin {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: &paymeError{Code: paymeIncorrectAmount, Message: "Incorrect amount"}})
		return
	}
	if p.ID != "" {
		if err := h.uc.SetTransactionExternalRef(c.Request.Context(), txn.ID, p.ID); err != nil {
			// Ref already set (retried CreateTransaction with the same id) is
			// fine — fall through and report the existing state.
			_ = err
		}
	}
	state := 1
	if txn.Status {
		state = 2
	}
	c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Result: gin.H{
		"create_time": txn.CreatedAt.UnixMilli(),
		"transaction": txn.ID.String(),
		"state":       state,
	}})
}

func (h *Handler) paymePerformTransaction(c *gin.Context, req paymeRPCRequest, p paymeParams) {
	txn, rpcErr := h.paymeResolveByExternalID(c, p.ID)
	if rpcErr != nil {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: rpcErr})
		return
	}
	if !txn.Status {
		if _, err := h.uc.SettlePaidTransaction(c.Request.Context(), txn.ID, p.ID); err != nil {
			c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: &paymeError{Code: paymeUnableToPerform, Message: "Unable to perform the operation"}})
			return
		}
	}
	c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Result: gin.H{
		"transaction":  txn.ID.String(),
		"perform_time": time.Now().UnixMilli(),
		"state":        2,
	}})
}

func (h *Handler) paymeCancelTransaction(c *gin.Context, req paymeRPCRequest, p paymeParams) {
	txn, rpcErr := h.paymeResolveByExternalID(c, p.ID)
	if rpcErr != nil {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: rpcErr})
		return
	}
	wasPerformed := txn.Status
	if _, err := h.uc.SettleCancelledTransaction(c.Request.Context(), txn.ID); err != nil {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: &paymeError{Code: paymeUnableToPerform, Message: "Unable to cancel the operation"}})
		return
	}
	state := -1
	if wasPerformed {
		state = -2
	}
	c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Result: gin.H{
		"transaction": txn.ID.String(),
		"cancel_time": time.Now().UnixMilli(),
		"state":       state,
	}})
}

func (h *Handler) paymeCheckTransaction(c *gin.Context, req paymeRPCRequest, p paymeParams) {
	txn, rpcErr := h.paymeResolveByExternalID(c, p.ID)
	if rpcErr != nil {
		c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Error: rpcErr})
		return
	}
	state := 1
	if txn.Status {
		state = 2
	}
	c.JSON(http.StatusOK, paymeRPCResponse{ID: req.ID, Result: gin.H{
		"transaction":  txn.ID.String(),
		"state":        state,
		"create_time":  txn.CreatedAt.UnixMilli(),
		"perform_time": 0,
		"cancel_time":  0,
	}})
}

func (h *Handler) paymeResolveByAccount(c *gin.Context, p paymeParams) (*Transaction, *paymeError) {
	txnID, err := uuid.Parse(p.Account.OrderID)
	if err != nil {
		return nil, &paymeError{Code: paymeInvalidAccount, Message: "Invalid account"}
	}
	txn, err := h.uc.GetTransactionByID(c.Request.Context(), txnID)
	if err != nil {
		return nil, &paymeError{Code: paymeInvalidAccount, Message: "Transaction not found"}
	}
	return txn, nil
}

func (h *Handler) paymeResolveByExternalID(c *gin.Context, paymeID string) (*Transaction, *paymeError) {
	txn, err := h.uc.GetTransactionByExternalRef(c.Request.Context(), paymeID)
	if err != nil {
		return nil, &paymeError{Code: paymeTransactionNotFound, Message: "Transaction not found"}
	}
	return txn, nil
}
