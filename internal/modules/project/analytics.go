package project

import (
	"context"
	"errors"
	"time"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

func NewUsecaseWithAnalytics(uc Usecase, repo AnalyticsRepository) Usecase {
	if u, ok := uc.(*usecase); ok {
		u.analytics = repo
	}
	return uc
}

func analyticsUnavailable() error {
	return httpx.Internal(errors.New("bot conversation analytics unavailable"))
}

func (u *usecase) populateAnalytics(ctx context.Context, items []MonitoringProject) error {
	if u.analytics == nil {
		return analyticsUnavailable()
	}
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID.String()
	}
	values, err := u.analytics.ReadAnalytics(ctx, ids, time.Now().UTC())
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Analytics = values[items[i].ID.String()]
	}
	return nil
}

func (u *usecase) monitoringItems(ctx context.Context, f AdminListFilter, all bool) ([]MonitoringProject, error) {
	if !all {
		return u.repo.ListMonitoring(ctx, f)
	}
	f.Limit, f.Offset = monitoringBatchSize, 0
	items := make([]MonitoringProject, 0)
	for {
		batch, err := u.repo.ListMonitoring(ctx, f)
		if err != nil {
			return nil, err
		}
		items = append(items, batch...)
		if len(batch) < int(f.Limit) {
			return items, nil
		}
		if f.Offset > 2147483647-f.Limit {
			return nil, errors.New("monitoring pagination overflow")
		}
		f.Offset += f.Limit
	}
}

func (u *usecase) AdminProjectStatistics(ctx context.Context, id uuid.UUID) (*BotAnalytics, error) {
	if _, err := u.AdminGet(ctx, id); err != nil {
		return nil, err
	}
	items := []MonitoringProject{{Project: Project{ID: id}}}
	if err := u.populateAnalytics(ctx, items); err != nil {
		return nil, httpx.Internal(err)
	}
	return &items[0].Analytics, nil
}

func (u *usecase) AdminBotStatistics(ctx context.Context, limit int32, ordering string) (*BotStatistics, error) {
	if ordering == "" {
		ordering = "activity_desc"
	}
	if ordering != "activity_desc" && ordering != "users_desc" {
		return nil, httpx.BadRequest("request.invalid", "ordering must be activity_desc or users_desc")
	}
	limit, _ = clampPage(limit, 0)
	items, err := u.monitoringItems(ctx, AdminListFilter{}, true)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if err := u.populateAnalytics(ctx, items); err != nil {
		return nil, httpx.Internal(err)
	}
	out := &BotStatistics{ProjectsTotal: int64(len(items))}
	for _, item := range items {
		a := item.Analytics
		out.Totals.UsersTotal += a.UsersTotal
		out.Totals.ActiveUsers24h += a.ActiveUsers24h
		out.Totals.ActiveUsers7d += a.ActiveUsers7d
		out.Totals.Messages24h += a.Messages24h
		if a.LastActivityAt != nil && (out.Totals.LastActivityAt == nil || a.LastActivityAt.After(*out.Totals.LastActivityAt)) {
			out.Totals.LastActivityAt = a.LastActivityAt
		}
	}
	sortMonitoringByResource(items, ordering)
	out.TopBots = pageSlice(items, 0, limit)
	return out, nil
}
