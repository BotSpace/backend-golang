package authbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// telegramTimeout bounds a single Telegram Bot API call, mirroring the
// 10-second budget the rest of this backend gives Telegram calls (see
// webhook.telegramTimeout, project.setReleaseWebhook).
const telegramTimeout = 10 * time.Second

// ReplyKeyboardMarkup is Telegram's reply-keyboard shape (Django:
// keyboards.auth.send_phone, a ReplyKeyboardMarkup with one
// request_contact button).
type ReplyKeyboardMarkup struct {
	Keyboard       [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard bool               `json:"resize_keyboard,omitempty"`
}

// KeyboardButton is one button of a ReplyKeyboardMarkup.
type KeyboardButton struct {
	Text           string `json:"text"`
	RequestContact bool   `json:"request_contact,omitempty"`
}

// InlineKeyboardMarkup is Telegram's inline-keyboard shape (Django:
// handlers.auth.phone_handler's InlineKeyboardMarkup with the "Kirish" URL
// button).
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// InlineKeyboardButton is one button of an InlineKeyboardMarkup.
type InlineKeyboardButton struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}

// SendOptions carries the optional sendMessage parameters this module uses.
type SendOptions struct {
	ParseMode   string // e.g. "Markdown" (Django: parse_mode="Markdown")
	ReplyMarkup any    // *ReplyKeyboardMarkup or *InlineKeyboardMarkup, or nil
}

// BotAPI is the minimal Telegram Bot API surface this module needs. It is an
// interface (rather than a concrete HTTP client) so webhook_test.go can
// inject a fake and assert on the outgoing sendMessage payload without
// making a real network call.
type BotAPI interface {
	// SendMessage posts a message to chatID and returns Telegram's
	// message_id (Django: bot.send_message(...).id, persisted via
	// code.message_id).
	SendMessage(ctx context.Context, chatID int64, text string, opts SendOptions) (messageID int64, err error)
}

// httpBotAPI is the real BotAPI, talking to https://api.telegram.org over
// stdlib net/http (mirrors project.setReleaseWebhook's plain http.Client
// pattern — no third-party Telegram SDK, same as the rest of this backend).
type httpBotAPI struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

// NewHTTPBotAPI builds the real Telegram client for the given bot token
// (Django: management.commands.base.bot, built from env BOT_TOKEN). An empty
// token yields a client whose calls always fail fast with a clear error
// instead of silently hitting Telegram with "bot<empty>/..." (mirrors
// Django's `bot = None` + "BOT_TOKEN not set — authbot disabled" logging).
func NewHTTPBotAPI(token string) BotAPI {
	return &httpBotAPI{
		token:      token,
		baseURL:    "https://api.telegram.org",
		httpClient: &http.Client{Timeout: telegramTimeout},
	}
}

type sendMessageRequest struct {
	ChatID      int64  `json:"chat_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type sendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

func (b *httpBotAPI) SendMessage(ctx context.Context, chatID int64, text string, opts SendOptions) (int64, error) {
	if b.token == "" {
		return 0, fmt.Errorf("authbot: BOT_TOKEN not set — bot disabled")
	}

	body, err := json.Marshal(sendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   opts.ParseMode,
		ReplyMarkup: opts.ReplyMarkup,
	})
	if err != nil {
		return 0, fmt.Errorf("authbot: marshal sendMessage request: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", b.baseURL, b.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("authbot: build sendMessage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("authbot: sendMessage call: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("authbot: read sendMessage response: %w", err)
	}

	var parsed sendMessageResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return 0, fmt.Errorf("authbot: decode sendMessage response (status %d): %w", resp.StatusCode, err)
	}
	if !parsed.OK {
		return 0, fmt.Errorf("authbot: sendMessage rejected (status %d): %s", resp.StatusCode, parsed.Description)
	}
	return parsed.Result.MessageID, nil
}
