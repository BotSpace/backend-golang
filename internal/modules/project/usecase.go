package project

import (
	"context"
	"errors"

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
	return p, nil
}

func (u *usecase) Get(ctx context.Context, id, ownerID uuid.UUID) (*Project, error) {
	p, err := u.repo.GetForOwner(ctx, id, ownerID)
	return mapErr(p, err)
}

func (u *usecase) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error) {
	p, err := u.repo.Update(ctx, id, ownerID, in)
	return mapErr(p, err)
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
