package authbot

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// TelegramUpdate is the subset of Telegram's Update object this module
// parses (Django: types.Update.de_json(json_string), then routed through
// telebot's message-handler dispatch to start_handler/phone_handler based on
// content). Unknown JSON fields are ignored.
type TelegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
}

// TelegramMessage is the subset of Telegram's Message object this module
// needs: enough to detect "/start" and a shared contact.
type TelegramMessage struct {
	MessageID int64            `json:"message_id"`
	From      *TelegramUser    `json:"from"`
	Chat      TelegramChat     `json:"chat"`
	Text      string           `json:"text"`
	Contact   *TelegramContact `json:"contact"`
}

// TelegramUser is Telegram's Update.message.from (Django:
// message.from_user).
type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// TelegramChat is Telegram's Update.message.chat (Django: message.chat).
type TelegramChat struct {
	ID int64 `json:"id"`
}

// TelegramContact is Telegram's Update.message.contact, present when the
// user tapped the "share phone" reply-keyboard button (Django:
// message.contact).
type TelegramContact struct {
	PhoneNumber string `json:"phone_number"`
	UserID      int64  `json:"user_id"`
}

// Bot reply text/labels, ported verbatim from core/apps/authbot/messages/auth.py
// and keyboards/auth.py — the dashboard's login UX depends on these exact
// strings, so do not reword them without updating the Django side too (or
// retiring it).
const (
	startWelcomeText    = "Assalomu Aleykum botga xush kelibsiz! 👋"
	sendPhoneButtonText = "Telefon nomerni yuborish 📲"
	otpMessageTemplate  = "Sizning tasdiqlash ko'dingiz: ```%s```\nAmal qilish muddati: *10 daqiqa*"
	loginButtonText     = "Kirish 🔐"
	loginURLPrefix      = "https://botspace.uz/uz/auth?callbackUrl=%2F&code="
	markdownParseMode   = "Markdown"
)

// sendPhoneKeyboard mirrors Django's keyboards.auth.send_phone: a single
// resize-to-fit reply-keyboard button that requests the user's contact.
var sendPhoneKeyboard = ReplyKeyboardMarkup{
	ResizeKeyboard: true,
	Keyboard:       [][]KeyboardButton{{{Text: sendPhoneButtonText, RequestContact: true}}},
}

// isStartCommand reports whether text is a Telegram "/start" command,
// mirroring telebot's commands=["start"] filter: "/start", "/start param"
// (deep-link payload), and "/start@BotUsername" (params) all match.
func isStartCommand(text string) bool {
	if text == "/start" {
		return true
	}
	return strings.HasPrefix(text, "/start ") || strings.HasPrefix(text, "/start@")
}

// processUpdate routes a parsed Telegram update to the same two handlers
// Django registers (bot.py): start_handler for "/start", phone_handler for a
// shared contact. Any other update is a silent no-op, matching telebot's
// default behavior when no registered handler matches.
func (h *Handler) processUpdate(ctx context.Context, update TelegramUpdate) {
	msg := update.Message
	if msg == nil {
		return
	}
	switch {
	case msg.Contact != nil:
		h.handleContact(ctx, msg)
	case isStartCommand(msg.Text):
		h.handleStart(ctx, msg.Chat.ID)
	}
}

// handleStart ports Django's handlers.auth.start_handler.
func (h *Handler) handleStart(ctx context.Context, chatID int64) {
	if _, err := h.bot.SendMessage(ctx, chatID, startWelcomeText, SendOptions{ReplyMarkup: sendPhoneKeyboard}); err != nil {
		log.Printf("authbot: send /start reply failed: %v", err)
	}
}

// handleContact ports Django's handlers.auth.phone_handler: upsert the bot
// user, (re)issue a login code via the same usecase POST /authbot/code
// already uses, send the code, then persist the message id it was sent in.
func (h *Handler) handleContact(ctx context.Context, msg *TelegramMessage) {
	if msg.From == nil || msg.Contact == nil {
		return
	}

	phone := strings.ReplaceAll(msg.Contact.PhoneNumber, "+", "")
	issued, err := h.uc.IssueCode(ctx, IssueCodeInput{
		TelegramID: msg.From.ID,
		Phone:      phone,
		FirstName:  msg.From.FirstName,
		LastName:   msg.From.LastName,
		Username:   msg.From.Username,
	})
	if err != nil {
		log.Printf("authbot: issue code failed: %v", err)
		h.sendPlainError(ctx, msg.Chat.ID, err)
		return
	}

	text := fmt.Sprintf(otpMessageTemplate, issued.Value)
	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{{{
			Text: loginButtonText,
			URL:  loginURLPrefix + issued.Value,
		}}},
	}

	messageID, err := h.bot.SendMessage(ctx, msg.Chat.ID, text, SendOptions{
		ParseMode:   markdownParseMode,
		ReplyMarkup: markup,
	})
	if err != nil {
		log.Printf("authbot: send otp message failed: %v", err)
		h.sendPlainError(ctx, msg.Chat.ID, err)
		return
	}

	if err := h.uc.SetMessageID(ctx, issued.CodeID, messageID); err != nil {
		log.Printf("authbot: set message id failed: %v", err)
	}
}

// sendPlainError ports phone_handler's catch-all except clause, which
// replies with the raw error text (Django: bot.send_message(chat.id,
// str(e))). Best-effort: a failure here is only logged.
func (h *Handler) sendPlainError(ctx context.Context, chatID int64, cause error) {
	if _, err := h.bot.SendMessage(ctx, chatID, cause.Error(), SendOptions{}); err != nil {
		log.Printf("authbot: send error notice failed: %v", err)
	}
}
