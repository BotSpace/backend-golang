// Package chatsend — celery send_telegram_message: CRM chat'dan foydalanuvchiga
// javob yuborish (matn yoki media). Celery'dagi 3x retry backoff bilan.
package chatsend

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/storagex"
	"github.com/botspace/backend/internal/shared/telegramx"
)

type Args struct {
	ChatID      int64  `json:"chat_id"`
	MessageType string `json:"message_type"`
	ProjectID   string `json:"project_id"`
	Text        string `json:"text"`
	FileID      string `json:"file_id"` // FileModel UUID (Telegram file_id EMAS)
}

type Usecase struct {
	Repo    Repository
	Storage *storagex.Storage
	Log     *zap.Logger
}

var mediaMethods = map[string][2]string{
	"photo":    {"sendPhoto", "photo"},
	"video":    {"sendVideo", "video"},
	"audio":    {"sendAudio", "audio"},
	"document": {"sendDocument", "document"},
	"file":     {"sendDocument", "document"},
}

func (u *Usecase) Handle(ctx context.Context, raw json.RawMessage) error {
	var args Args
	if err := json.Unmarshal(raw, &args); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second) // 2s, 4s — celery countdown=2**retries
		}
		if lastErr = u.send(ctx, args); lastErr == nil {
			return nil
		}
		u.Log.Warn("chatsend: attempt failed", zap.Int("attempt", attempt+1), zap.Error(lastErr))
	}
	u.Log.Error("chatsend: gave up", zap.Int64("chat", args.ChatID), zap.Error(lastErr))
	return nil // retry tugadi — stream'da pending qoldirmaymiz
}

func (u *Usecase) send(ctx context.Context, args Args) error {
	projectID, err := database.ParseUUID(args.ProjectID)
	if err != nil {
		return fmt.Errorf("invalid project_id: %w", err)
	}
	token, err := u.Repo.GetProjectEnv(ctx, projectID, "RELEASE_BOT_TOKEN")
	if err != nil {
		token, err = u.Repo.GetProjectEnv(ctx, projectID, "BOT_TOKEN")
		if err != nil {
			return fmt.Errorf("bot token yo'q (project=%s)", args.ProjectID)
		}
	}
	tg := telegramx.New(token)

	m, ok := mediaMethods[args.MessageType]
	if !ok {
		_, err := tg.SendMessage(ctx, args.ChatID, orDefault(args.Text, "No message"), "HTML", nil)
		return err
	}

	if args.FileID == "" {
		return fmt.Errorf("media uchun file_id yo'q")
	}
	fileID, err := database.ParseUUID(args.FileID)
	if err != nil {
		return fmt.Errorf("invalid file_id: %w", err)
	}
	path, err := u.Repo.GetFilePath(ctx, fileID)
	if err != nil {
		return fmt.Errorf("file topilmadi id=%s: %w", args.FileID, err)
	}
	content, err := u.Storage.Read(ctx, path)
	if err != nil {
		return fmt.Errorf("storage read: %w", err)
	}
	_, err = tg.SendMediaUpload(ctx, m[0], m[1], args.ChatID, path, content, args.Text, nil)
	return err
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
