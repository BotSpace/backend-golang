package webhook

import (
	"fmt"
	"os"
	"time"
)

// hookURL builds the public webhook-hook URL for a token — the Go
// webhook-hook service (outside this API) resolves it via ResolveToken and
// forwards the call to the running engine. Mirrors Django's _hook_url.
func hookURL(token string) string {
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "api.botspace.uz"
	}
	return fmt.Sprintf("https://%s/api/hook/%s/", domain, token)
}

// MintRequest is the owner-facing payload to mint (or reuse) a webhook URL
// for one flow node, optionally bound to a chat.
type MintRequest struct {
	Project string `json:"project" validate:"required"`
	NodeID  string `json:"node_id" validate:"required,max=255"`
	ChatID  *int64 `json:"chat_id" validate:"omitempty"`
}

// MintResponse mirrors WebhookEndpointMintView's {token, url, chat_id}.
type MintResponse struct {
	Token  string `json:"token"`
	URL    string `json:"url"`
	ChatID int64  `json:"chat_id"`
}

func toMintResponse(e *WebhookEndpoint) MintResponse {
	return MintResponse{
		Token:  e.Token.String(),
		URL:    hookURL(e.Token.String()),
		ChatID: e.ChatID,
	}
}

// ResolveResponse mirrors WebhookHookResolveView's routing payload.
type ResolveResponse struct {
	Token              string `json:"token"`
	NodeID             string `json:"node_id"`
	ChatID             int64  `json:"chat_id"`
	ProjectID          string `json:"project_id"`
	ReleaseWebhookUUID string `json:"release_webhook_uuid"`
	DebugWebhookUUID   string `json:"debug_webhook_uuid"`
}

func toResolveResponse(h *ResolvedHook) ResolveResponse {
	return ResolveResponse{
		Token:              h.Token.String(),
		NodeID:             h.NodeID,
		ChatID:             h.ChatID,
		ProjectID:          h.ProjectID.String(),
		ReleaseWebhookUUID: h.ReleaseWebhookUUID.String(),
		DebugWebhookUUID:   h.DebugWebhookUUID.String(),
	}
}

// ManagedBotResponse is the owner-facing projection of a managed bot — the
// token is never exposed, only whether the bot is available (used == false).
type ManagedBotResponse struct {
	ID        string    `json:"id"`
	TgBotID   int64     `json:"tg_bot_id"`
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Used      bool      `json:"used"`
}

func toManagedBotResponse(b *ManagedBot) ManagedBotResponse {
	return ManagedBotResponse{
		ID:        b.ID.String(),
		TgBotID:   b.TgBotID,
		Username:  b.Username,
		Name:      b.Name,
		CreatedAt: b.CreatedAt,
		Used:      b.Used,
	}
}

func toManagedBotListResponse(bots []ManagedBot) []ManagedBotResponse {
	out := make([]ManagedBotResponse, len(bots))
	for i := range bots {
		out[i] = toManagedBotResponse(&bots[i])
	}
	return out
}

// ManagedBotConfigResponse mirrors ManagedBotConfigView.
type ManagedBotConfigResponse struct {
	Enabled          bool   `json:"enabled"`
	ManagerUsername  string `json:"manager_username,omitempty"`
	DeepLinkTemplate string `json:"deep_link_template,omitempty"`
}

func toManagedBotConfigResponse(cfg ManagedBotConfig) ManagedBotConfigResponse {
	return ManagedBotConfigResponse{
		Enabled:          cfg.Enabled,
		ManagerUsername:  cfg.ManagerUsername,
		DeepLinkTemplate: cfg.DeepLinkTemplate,
	}
}

// ManagedBotClaimRequest is the wizard's pre-claim payload.
type ManagedBotClaimRequest struct {
	Username string `json:"username" validate:"required"`
}

// managedBotWebhookBody is Telegram's "managed_bot" update shape, as posted
// by the manager bot to POST /managed-bot/webhook/<secret>/.
type managedBotWebhookBody struct {
	ManagedBot *struct {
		Bot struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
		} `json:"bot"`
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	} `json:"managed_bot"`
}

func toWebhookPayload(body managedBotWebhookBody) ManagedBotWebhookPayload {
	if body.ManagedBot == nil {
		return ManagedBotWebhookPayload{}
	}
	var p ManagedBotWebhookPayload
	p.Bot.ID = body.ManagedBot.Bot.ID
	p.Bot.Username = body.ManagedBot.Bot.Username
	p.Bot.FirstName = body.ManagedBot.Bot.FirstName
	p.CreatorID = body.ManagedBot.User.ID
	return p
}
