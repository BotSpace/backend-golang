package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Usecase exposes owner-scoped project operations, plus admin-wide
// (unscoped) listing/detail/monitoring reachable only behind an admin route.
type Usecase interface {
	Create(ctx context.Context, in CreateInput) (*Project, error)
	Get(ctx context.Context, id, ownerID uuid.UUID) (*Project, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error)
	Delete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Project, int64, error)

	AdminList(ctx context.Context, f AdminListFilter) ([]AdminProjectSummary, int64, error)
	AdminGet(ctx context.Context, id uuid.UUID) (*AdminProjectDetail, error)
	AdminMonitoring(ctx context.Context, f AdminListFilter) ([]MonitoringProject, int64, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

func (u *usecase) Create(ctx context.Context, in CreateInput) (*Project, error) {
	p, err := u.repo.Create(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if err := u.applyToken(ctx, p, p.OwnerID, in.Token); err != nil {
		return nil, err
	}
	return p, nil
}

func (u *usecase) Get(ctx context.Context, id, ownerID uuid.UUID) (*Project, error) {
	p, err := u.repo.GetForOwner(ctx, id, ownerID)
	return mapErr(p, err)
}

func (u *usecase) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error) {
	p, err := u.repo.Update(ctx, id, ownerID, in)
	if p, err = mapErr(p, err); err != nil {
		return nil, err
	}
	var token string
	if in.Token != nil {
		token = *in.Token
	}
	if err := u.applyToken(ctx, p, ownerID, token); err != nil {
		return nil, err
	}
	return p, nil
}

// applyToken stores a non-blank Telegram bot token as the project's
// RELEASE_BOT_TOKEN environment variable and best-effort registers Telegram's
// webhook for it — mirrors Django's EnviromentModel.objects.update_or_create
// (bot.py:39) + core/apps/api/services/webhook.py's set_webhook(instance,
// "release"). A blank token is a no-op: Create/Update never force a token,
// only store one when given (dashboard's bot_token_required flow handles the
// tokenless path at release-deploy time instead).
func (u *usecase) applyToken(ctx context.Context, p *Project, ownerID uuid.UUID, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	if err := u.repo.SetReleaseBotToken(ctx, p.ID, ownerID, token); err != nil {
		return httpx.Internal(fmt.Errorf("store release bot token: %w", err))
	}
	setReleaseWebhook(token, p.ReleaseWebhookUUID)
	return nil
}

// setReleaseWebhook registers this project's stable release webhook route
// with Telegram for the given token. Best-effort by design (mirrors Django's
// set_webhook, which swallows httpx errors and logs — see webhook.py:43-50):
// a bad token or an unreachable Telegram API must never fail project
// create/update, since the release deploy path retries this same
// registration (release.Usecase.CreateRelease -> engine deploy) anyway.
func setReleaseWebhook(token string, releaseWebhookUUID uuid.UUID) {
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "api.botspace.uz"
	}
	hookURL := fmt.Sprintf("https://%s/api/bot-webhook/release/%s/", domain, releaseWebhookUUID.String())
	telegramURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook?url=%s", token, hookURL)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodPost, telegramURL, nil)
	if err != nil {
		log.Printf("project: build setWebhook request failed: %v", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("project: setWebhook call failed (will retry on release deploy): %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("project: setWebhook returned status %d", resp.StatusCode)
	}
}

func (u *usecase) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	if err := u.repo.SoftDelete(ctx, id, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("project.not_found", "project not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) List(ctx context.Context, f ListFilter) ([]Project, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.List(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.Count(ctx, f.OwnerID, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func clampPage(limit, offset int32) (int32, int32) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// AdminList returns a page of every project across every owner (admin-only).
func (u *usecase) AdminList(ctx context.Context, f AdminListFilter) ([]AdminProjectSummary, int64, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	items, err := u.repo.ListAdmin(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountAdmin(ctx, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// AdminGet returns any project's detail (owner + release history) regardless
// of who owns it (admin-only).
func (u *usecase) AdminGet(ctx context.Context, id uuid.UUID) (*AdminProjectDetail, error) {
	p, err := u.repo.GetAdmin(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("project.not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return p, nil
}

// AdminMonitoring returns a page of every project across every owner with
// its latest reported resource usage (admin-only).
func (u *usecase) AdminMonitoring(ctx context.Context, f AdminListFilter) ([]MonitoringProject, int64, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	items, err := u.repo.ListMonitoring(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountAdmin(ctx, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// mapErr translates a not-found into a 404 and any other error into a 500.
func mapErr(p *Project, err error) (*Project, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("project.not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return p, nil
}
