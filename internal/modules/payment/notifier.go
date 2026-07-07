package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const defaultEngineCallbackTimeout = 5 * time.Second

type httpNotifier struct {
	repo          Repository
	internalToken string
	client        *http.Client
}

func NewHTTPNotifier(repo Repository, internalToken string) Notifier {
	return &httpNotifier{
		repo:          repo,
		internalToken: internalToken,
		client:        &http.Client{Timeout: defaultEngineCallbackTimeout},
	}
}

type paymentCallbackRequest struct {
	OrderID    string `json:"order_id"`
	ChatID     int64  `json:"chat_id"`
	Status     string `json:"status"`
	Amount     string `json:"amount"`
	Net        string `json:"net"`
	Commission string `json:"commission"`
	Comment    string `json:"comment"`
	Provider   string `json:"provider"`
}

func (n *httpNotifier) NotifyOrderPaid(ctx context.Context, order *Order) error {
	if order == nil {
		return nil
	}
	if n.internalToken == "" {
		return fmt.Errorf("payment notifier: internal token is empty")
	}
	uuids, err := n.repo.GetProjectWebhookUUIDs(ctx, order.ProjectID)
	if err != nil {
		return err
	}
	targetUUID := uuids.Release
	if order.IsDebug {
		targetUUID = uuids.Debug
	}
	if targetUUID == uuid.Nil {
		return fmt.Errorf("payment notifier: project %s has empty callback uuid", order.ProjectID)
	}

	body, err := json.Marshal(paymentCallbackRequest{
		OrderID:    order.ID.String(),
		ChatID:     order.PayerChatID,
		Status:     order.Status,
		Amount:     order.Amount,
		Net:        order.NetAmount,
		Commission: order.Commission,
		Comment:    order.Comment,
		Provider:   order.Provider,
	})
	if err != nil {
		return fmt.Errorf("payment notifier: marshal callback: %w", err)
	}

	url := fmt.Sprintf("http://bot-%s:8000/payment-callback", targetUUID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("payment notifier: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", n.internalToken)

	client := n.client
	if client == nil {
		client = &http.Client{Timeout: defaultEngineCallbackTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("payment notifier: post callback: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("payment notifier: callback returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
