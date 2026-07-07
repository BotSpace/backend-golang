package payment

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultPaycueBaseURL = "https://paycue.uz"

type PaycueGateway struct {
	BaseURL       string
	Token         string
	WebhookSecret string
	CardID        int64
	HTTPClient    *http.Client
}

type PaycueTransaction struct {
	Amount        string
	CardID        int64
	TransactionID string
	PayURL        string
}

func NewPaycueGatewayFromEnv() PaycueGateway {
	cardID, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("APP_PAYCUE_CARD_ID")), 10, 64)
	return PaycueGateway{
		BaseURL:       envOrDefault("APP_PAYCUE_BASE_URL", defaultPaycueBaseURL),
		Token:         os.Getenv("APP_PAYCUE_TOKEN"),
		WebhookSecret: os.Getenv("APP_PAYCUE_WEBHOOK_SECRET"),
		CardID:        cardID,
		HTTPClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (g PaycueGateway) CreateTransaction(ctx context.Context, amount string) (*PaycueTransaction, error) {
	if g.Token == "" {
		return nil, fmt.Errorf("paycue token is not configured")
	}
	amountSom, err := amountStringToWholeSom(amount)
	if err != nil {
		return nil, err
	}
	reqBody := map[string]interface{}{"amount": amountSom}
	if g.CardID > 0 {
		reqBody["card_id"] = g.CardID
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("paycue marshal request: %w", err)
	}

	url := strings.TrimRight(g.BaseURL, "/") + "/api/transactions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("paycue build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.Token)

	client := g.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paycue create transaction: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var env struct {
		Status bool `json:"status"`
		Data   struct {
			Amount        json.Number `json:"amount"`
			CardID        int64       `json:"card_id"`
			TransactionID string      `json:"transaction_id"`
			PayURL        string      `json:"pay_url"`
			Detail        string      `json:"detail"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("paycue decode response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Status {
		detail := env.Data.Detail
		if detail == "" {
			detail = strings.TrimSpace(string(respBody))
		}
		return nil, fmt.Errorf("paycue returned %d: %s", resp.StatusCode, detail)
	}
	if env.Data.TransactionID == "" {
		return nil, fmt.Errorf("paycue response missing transaction_id")
	}
	amountOut := paycueAmountToMoneyString(env.Data.Amount)
	payURL := env.Data.PayURL
	if payURL == "" {
		payURL = strings.TrimRight(g.BaseURL, "/") + "/pay/" + env.Data.TransactionID
	}
	return &PaycueTransaction{
		Amount:        amountOut,
		CardID:        env.Data.CardID,
		TransactionID: env.Data.TransactionID,
		PayURL:        payURL,
	}, nil
}

func (g PaycueGateway) PayURL(transactionID string) string {
	if transactionID == "" {
		return ""
	}
	return strings.TrimRight(firstNonEmpty(g.BaseURL, defaultPaycueBaseURL), "/") + "/pay/" + transactionID
}

func (g PaycueGateway) verifyWebhookSecret(header string) bool {
	if g.WebhookSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(g.WebhookSecret)) == 1
}

func amountStringToWholeSom(amount string) (int64, error) {
	cents, err := parseMoneyCents(amount)
	if err != nil {
		return 0, err
	}
	if cents <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	if cents%100 != 0 {
		return 0, fmt.Errorf("paycue amount must be whole som")
	}
	return cents / 100, nil
}

func paycueAmountToMoneyString(n json.Number) string {
	if n.String() == "" {
		return "0.00"
	}
	if i, err := n.Int64(); err == nil {
		return fmt.Sprintf("%d.00", i)
	}
	if f, err := strconv.ParseFloat(n.String(), 64); err == nil {
		return fmt.Sprintf("%.2f", f)
	}
	return n.String()
}
