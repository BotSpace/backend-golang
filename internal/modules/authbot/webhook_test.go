package authbot

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/botspace/backend/internal/modules/user"
	"github.com/gin-gonic/gin"
)

// fakeBotAPI records every SendMessage call instead of hitting Telegram, so
// tests can assert on the exact outgoing payload (text/parse_mode/markup)
// without a network call.
type fakeBotAPI struct {
	calls  []sentMessage
	nextID int64
}

type sentMessage struct {
	ChatID int64
	Text   string
	Opts   SendOptions
}

func (f *fakeBotAPI) SendMessage(_ context.Context, chatID int64, text string, opts SendOptions) (int64, error) {
	f.calls = append(f.calls, sentMessage{ChatID: chatID, Text: text, Opts: opts})
	f.nextID++
	return f.nextID, nil
}

func newTestHandler() (*Handler, *fakeAuthbotRepo, *fakeBotAPI) {
	authbotRepo := newFakeAuthbotRepo()
	userRepo := newFakeUserRepo()
	uc := NewUsecase(authbotRepo, user.NewUsecase(userRepo), fakeTokens{})
	bot := &fakeBotAPI{}
	return NewHandler(uc, bot), authbotRepo, bot
}

func postWebhook(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/authbot/webhook", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.Webhook(c)
	return w
}

// TestWebhookStartSendsContactRequestKeyboard ports Django's start_handler:
// a bare "/start" update must reply with the welcome text and a
// request-contact reply keyboard, and must not touch the DB (no code
// issued).
func TestWebhookStartSendsContactRequestKeyboard(t *testing.T) {
	h, repo, bot := newTestHandler()

	update := TelegramUpdate{
		UpdateID: 1,
		Message: &TelegramMessage{
			MessageID: 10,
			From:      &TelegramUser{ID: 555, FirstName: "Ali"},
			Chat:      TelegramChat{ID: 555},
			Text:      "/start",
		},
	}

	w := postWebhook(t, h, update)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(bot.calls) != 1 {
		t.Fatalf("expected exactly 1 sendMessage call, got %d", len(bot.calls))
	}
	call := bot.calls[0]
	if call.ChatID != 555 {
		t.Fatalf("expected reply to chat 555, got %d", call.ChatID)
	}
	if call.Text != startWelcomeText {
		t.Fatalf("unexpected welcome text: %q", call.Text)
	}
	markup, ok := call.Opts.ReplyMarkup.(ReplyKeyboardMarkup)
	if !ok {
		t.Fatalf("expected a ReplyKeyboardMarkup, got %T", call.Opts.ReplyMarkup)
	}
	if !markup.ResizeKeyboard {
		t.Fatal("expected resize_keyboard=true")
	}
	if len(markup.Keyboard) != 1 || len(markup.Keyboard[0]) != 1 {
		t.Fatalf("expected a single contact-request button, got %+v", markup.Keyboard)
	}
	btn := markup.Keyboard[0][0]
	if btn.Text != sendPhoneButtonText || !btn.RequestContact {
		t.Fatalf("unexpected contact-request button: %+v", btn)
	}

	if len(repo.botUsers) != 0 || len(repo.codes) != 0 {
		t.Fatal("expected /start to not touch bot users or codes")
	}
}

// TestWebhookContactIssuesCodeAndSendsOTP ports Django's phone_handler: a
// shared-contact update must upsert the bot user, issue a code (via the same
// usecase POST /authbot/code uses), send it with the "Kirish" inline button,
// and persist the resulting message id.
func TestWebhookContactIssuesCodeAndSendsOTP(t *testing.T) {
	h, repo, bot := newTestHandler()

	update := TelegramUpdate{
		UpdateID: 2,
		Message: &TelegramMessage{
			MessageID: 11,
			From:      &TelegramUser{ID: 777, FirstName: "Vali", LastName: "Aliyev", Username: "vali"},
			Chat:      TelegramChat{ID: 777},
			Contact:   &TelegramContact{PhoneNumber: "+998901234567", UserID: 777},
		},
	}

	w := postWebhook(t, h, update)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(repo.botUsers) != 1 {
		t.Fatalf("expected exactly 1 bot user upserted, got %d", len(repo.botUsers))
	}
	var code *Code
	for _, c := range repo.codes {
		code = c
	}
	if code == nil {
		t.Fatal("expected a code to be issued")
	}

	if len(bot.calls) != 1 {
		t.Fatalf("expected exactly 1 sendMessage call, got %d", len(bot.calls))
	}
	call := bot.calls[0]
	if call.ChatID != 777 {
		t.Fatalf("expected reply to chat 777, got %d", call.ChatID)
	}
	wantText := "Sizning tasdiqlash ko'dingiz: ```" + code.Value + "```\nAmal qilish muddati: *10 daqiqa*"
	if call.Text != wantText {
		t.Fatalf("unexpected OTP text: got %q want %q", call.Text, wantText)
	}
	if call.Opts.ParseMode != "Markdown" {
		t.Fatalf("expected Markdown parse mode, got %q", call.Opts.ParseMode)
	}
	markup, ok := call.Opts.ReplyMarkup.(InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("expected an InlineKeyboardMarkup, got %T", call.Opts.ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("expected a single login button, got %+v", markup.InlineKeyboard)
	}
	btn := markup.InlineKeyboard[0][0]
	if btn.Text != loginButtonText {
		t.Fatalf("unexpected login button text: %q", btn.Text)
	}
	wantURL := "https://botspace.uz/uz/auth?callbackUrl=%2F&code=" + code.Value
	if btn.URL != wantURL {
		t.Fatalf("unexpected login URL: got %q want %q", btn.URL, wantURL)
	}

	// The message id from the sendMessage call must be persisted on the code
	// (Django: code.message_id = sent_message.id).
	if code.MessageID == nil || *code.MessageID != bot.nextID {
		t.Fatalf("expected code.message_id=%d, got %+v", bot.nextID, code.MessageID)
	}

	// Phone must have the leading "+" stripped, mirroring Django's
	// str(message.contact.phone_number).replace("+", "").
	var bu *BotUser
	for _, b := range repo.botUsers {
		bu = b
	}
	if bu.Phone != "998901234567" {
		t.Fatalf("expected phone with '+' stripped, got %q", bu.Phone)
	}
}

// TestWebhookUnhandledUpdateIsNoop mirrors telebot's default behavior: an
// update matching neither registered handler (no /start, no contact) is
// acked 200 with no side effects.
func TestWebhookUnhandledUpdateIsNoop(t *testing.T) {
	h, repo, bot := newTestHandler()

	update := TelegramUpdate{
		UpdateID: 3,
		Message: &TelegramMessage{
			MessageID: 12,
			From:      &TelegramUser{ID: 888},
			Chat:      TelegramChat{ID: 888},
			Text:      "hello",
		},
	}

	w := postWebhook(t, h, update)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(bot.calls) != 0 {
		t.Fatalf("expected no sendMessage calls, got %d", len(bot.calls))
	}
	if len(repo.botUsers) != 0 || len(repo.codes) != 0 {
		t.Fatal("expected no side effects for an unhandled update")
	}
}

// TestWebhookMalformedBodyIsBadRequest guards against Telegram (or a bad
// actor) posting non-JSON to the webhook.
func TestWebhookMalformedBodyIsBadRequest(t *testing.T) {
	h, _, _ := newTestHandler()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/authbot/webhook", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.Webhook(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", w.Code)
	}
}
