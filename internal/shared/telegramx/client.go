// Package telegramx — Telegram Bot API'ning worker'ga kerakli MINIMAL qismi.
// Kutubxona emas, oddiy HTTP: sendMessage/sendPhoto/sendVideo/sendAudio/
// sendDocument (file upload bilan), getMe, getUserProfilePhotos, getFile,
// fayl yuklab olish.
package telegramx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.telegram.org"

type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

type apiResponse struct {
	Ok          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/%s", apiBase, c.token, method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("telegram: status %d, content-type %q, body %q", resp.StatusCode, contentType, bodySnippet(body))
	}

	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return fmt.Errorf("telegram: invalid JSON response, status %d, content-type %q, body %q: %w", resp.StatusCode, contentType, bodySnippet(body), err)
	}
	if !ar.Ok {
		return fmt.Errorf("telegram: %s", ar.Description)
	}
	if out != nil {
		return json.Unmarshal(ar.Result, out)
	}
	return nil
}

func bodySnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 240 {
		s = s[:240] + "..."
	}
	return s
}

// --- Yuboriladigan tiplar ---

type InlineButton struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

type ReplyButton struct {
	Text            string `json:"text"`
	RequestContact  bool   `json:"request_contact,omitempty"`
	RequestLocation bool   `json:"request_location,omitempty"`
}

// Keyboard — sendMessage reply_markup uchun tayyor obyekt (inline yoki reply).
type Keyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard,omitempty"`
	Keyboard       [][]ReplyButton  `json:"keyboard,omitempty"`
	ResizeKeyboard bool             `json:"resize_keyboard,omitempty"`
}

type Message struct {
	MessageID int64 `json:"message_id"`
	Photo     []struct {
		FileID string `json:"file_id"`
	} `json:"photo"`
	Video *struct {
		FileID string `json:"file_id"`
	} `json:"video"`
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, parseMode string, kb *Keyboard) (*Message, error) {
	payload := map[string]any{"chat_id": chatID, "text": text}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	var m Message
	if err := c.call(ctx, "sendMessage", payload, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SendMediaByFileID — file_id bilan media yuborish (JSON body yetadi).
func (c *Client) SendMediaByFileID(ctx context.Context, method, field string, chatID int64, fileID, caption string, kb *Keyboard) (*Message, error) {
	payload := map[string]any{"chat_id": chatID, field: fileID}
	if caption != "" {
		payload["caption"] = caption
		payload["parse_mode"] = "HTML"
	}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	var m Message
	if err := c.call(ctx, method, payload, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SendMediaUpload — faylni multipart bilan yuklab yuborish (birinchi yuborishda).
func (c *Client) SendMediaUpload(ctx context.Context, method, field string, chatID int64, filename string, content []byte, caption string, kb *Keyboard) (*Message, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	if caption != "" {
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "HTML")
	}
	if kb != nil {
		kbJSON, err := json.Marshal(kb)
		if err != nil {
			return nil, err
		}
		_ = w.WriteField("reply_markup", string(kbJSON))
	}
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(content); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/%s", apiBase, c.token, method), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m Message
	if err := decode(resp, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// --- sync_bot uchun ---

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", map[string]any{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

type profilePhotos struct {
	Photos [][]struct {
		FileID string `json:"file_id"`
	} `json:"photos"`
}

// LatestProfilePhotoFileID — botning eng so'nggi profil rasmi file_id'si ("" = rasm yo'q).
func (c *Client) LatestProfilePhotoFileID(ctx context.Context, userID int64) (string, error) {
	var pp profilePhotos
	if err := c.call(ctx, "getUserProfilePhotos", map[string]any{"user_id": userID}, &pp); err != nil {
		return "", err
	}
	if len(pp.Photos) == 0 || len(pp.Photos[0]) == 0 {
		return "", nil
	}
	sizes := pp.Photos[0]
	return sizes[len(sizes)-1].FileID, nil
}

// DownloadFile — getFile + file yuklab olish.
func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/file/bot%s/%s", apiBase, c.token, f.FilePath), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram file download: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
