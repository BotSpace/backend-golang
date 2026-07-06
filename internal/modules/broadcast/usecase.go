// Package broadcast — celery send_message: broadcast xabarini loyihaning barcha
// foydalanuvchilariga (legacy Mongo users kolleksiyasi) yuboradi. file_id kesh,
// rate-limit va statistika backend'dagi bilan bir xil.
package broadcast

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/mongox"
	"github.com/botspace/backend/internal/shared/storagex"
	"github.com/botspace/backend/internal/shared/telegramx"
)

type Args struct {
	MessageID string `json:"message_id"`
}

// Django MessageModel.buttons: 2D massiv [{title, link?, callback_data?,
// request_contact?, request_location?}].
type button struct {
	Title           string `json:"title"`
	Link            string `json:"link"`
	CallbackData    string `json:"callback_data"`
	RequestContact  bool   `json:"request_contact"`
	RequestLocation bool   `json:"request_location"`
}

type Usecase struct {
	Repo    Repository
	Mongo   *mongox.Client
	Storage *storagex.Storage
	Log     *zap.Logger
}

func (u *Usecase) Handle(ctx context.Context, raw json.RawMessage) error {
	var args Args
	if err := json.Unmarshal(raw, &args); err != nil {
		return err
	}
	messageID, err := database.ParseUUID(args.MessageID)
	if err != nil {
		return err
	}
	msg, err := u.Repo.GetMessage(ctx, messageID)
	if err != nil {
		u.Log.Error("broadcast: message topilmadi", zap.String("id", args.MessageID), zap.Error(err))
		return nil // qayta urinish befoyda
	}

	fail := func() {
		_ = u.Repo.MarkMessageFailed(ctx, msg.ID)
	}

	hasUsers, err := u.Repo.HasUsersCollection(ctx, msg.ProjectID)
	if err != nil || !hasUsers {
		u.Log.Error("broadcast: users kolleksiyasi yo'q", zap.String("project", database.UUIDString(msg.ProjectID)))
		fail()
		return nil
	}

	token, err := u.Repo.GetProjectEnv(ctx, msg.ProjectID, "RELEASE_BOT_TOKEN")
	if err != nil {
		// backend release_bot_token fallback: BOT_TOKEN (debug)
		token, err = u.Repo.GetProjectEnv(ctx, msg.ProjectID, "BOT_TOKEN")
		if err != nil {
			u.Log.Error("broadcast: bot token yo'q", zap.String("project", database.UUIDString(msg.ProjectID)))
			fail()
			return nil
		}
	}
	tg := telegramx.New(token)

	if msg.ProjectLegacyID == nil {
		u.Log.Error("broadcast: project legacy_id yo'q", zap.String("project", database.UUIDString(msg.ProjectID)))
		fail()
		return nil
	}
	users, err := u.Mongo.ProjectUsers(ctx, int64(*msg.ProjectLegacyID))
	if err != nil {
		u.Log.Error("broadcast: mongo users", zap.String("project", database.UUIDString(msg.ProjectID)), zap.Error(err))
		fail()
		return nil
	}

	total := int64(len(users))
	_ = u.Repo.UpdateMessageTotal(ctx, msg.ID, total)

	kb := buildKeyboard(msg.ButtonType, msg.Buttons)
	var cachedFileID string
	if msg.FileID != nil {
		cachedFileID = *msg.FileID
	}

	var done, failed int64
	for _, user := range users {
		chatID, ok := chatIDOf(user)
		if !ok {
			failed++
			continue
		}
		sent, newFileID, err := u.sendOne(ctx, tg, msg, chatID, cachedFileID, kb)
		if err != nil {
			failed++
			u.Log.Debug("broadcast: send failed", zap.Int64("chat", chatID), zap.Error(err))
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if !sent {
			failed++
			continue
		}
		if newFileID != "" && cachedFileID == "" {
			cachedFileID = newFileID
			_ = u.Repo.UpdateMessageFileID(ctx, msg.ID, cachedFileID)
		}
		done++
		time.Sleep(50 * time.Millisecond) // ~20 msg/s — Telegram limitidan pastda
	}

	status := "progress"
	switch {
	case done == total:
		status = "done"
	case failed == total:
		status = "failed"
	}
	_ = u.Repo.UpdateMessageResult(ctx, msg.ID, done, failed, status)
	u.Log.Info("broadcast done", zap.String("message", database.UUIDString(msg.ID)),
		zap.Int64("done", done), zap.Int64("fail", failed), zap.Int64("total", total))
	return nil
}

func (u *Usecase) sendOne(ctx context.Context, tg *telegramx.Client, msg Message, chatID int64, cachedFileID string, kb *telegramx.Keyboard) (sent bool, newFileID string, err error) {
	switch msg.MessageType {
	case "photo", "video":
		method, field := "sendPhoto", "photo"
		if msg.MessageType == "video" {
			method, field = "sendVideo", "video"
		}
		if cachedFileID != "" {
			_, err = tg.SendMediaByFileID(ctx, method, field, chatID, cachedFileID, msg.Caption, kb)
			return err == nil, "", err
		}
		return false, "", nil
	default:
		_, err = tg.SendMessage(ctx, chatID, msg.Caption, "", kb)
		return err == nil, "", err
	}
}

func buildKeyboard(buttonType string, rawButtons []byte) *telegramx.Keyboard {
	var rows [][]button
	if err := json.Unmarshal(rawButtons, &rows); err != nil || len(rows) == 0 {
		return nil
	}
	kb := &telegramx.Keyboard{}
	if buttonType == "inline" {
		for _, row := range rows {
			var line []telegramx.InlineButton
			for _, b := range row {
				switch {
				case b.Link != "":
					line = append(line, telegramx.InlineButton{Text: b.Title, URL: b.Link})
				case b.CallbackData != "":
					line = append(line, telegramx.InlineButton{Text: b.Title, CallbackData: b.CallbackData})
				}
			}
			if len(line) > 0 {
				kb.InlineKeyboard = append(kb.InlineKeyboard, line)
			}
		}
		if len(kb.InlineKeyboard) == 0 {
			return nil
		}
		return kb
	}
	kb.ResizeKeyboard = true
	for _, row := range rows {
		var line []telegramx.ReplyButton
		for _, b := range row {
			line = append(line, telegramx.ReplyButton{
				Text:            b.Title,
				RequestContact:  b.RequestContact,
				RequestLocation: b.RequestLocation,
			})
		}
		if len(line) > 0 {
			kb.Keyboard = append(kb.Keyboard, line)
		}
	}
	if len(kb.Keyboard) == 0 {
		return nil
	}
	return kb
}

// chatIDOf — mongo hujjatidagi chat_id (int32/int64/float64 bo'lishi mumkin).
func chatIDOf(doc map[string]any) (int64, bool) {
	switch v := doc["chat_id"].(type) {
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}
