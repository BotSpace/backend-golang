package file

import (
	"context"
	"errors"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Usecase exposes owner-scoped file/resource operations plus the aggregated
// "used" read.
type Usecase interface {
	CreateFile(ctx context.Context, in CreateFileInput) (*File, error)
	GetFilePublic(ctx context.Context, uid uuid.UUID) (*File, error)
	ProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error)
	GetFileForOwner(ctx context.Context, uid, ownerID uuid.UUID) (*File, error)
	UpdateFile(ctx context.Context, uid, ownerID uuid.UUID, in UpdateFileInput) (*File, error)
	DeleteFile(ctx context.Context, uid, ownerID uuid.UUID) (*File, error)
	ListFiles(ctx context.Context, f ListFileFilter) ([]File, int64, error)

	CreateResource(ctx context.Context, in CreateResourceInput) (*Resource, error)
	GetResource(ctx context.Context, id, ownerID uuid.UUID) (*Resource, error)
	ListResources(ctx context.Context, f ListResourceFilter) ([]Resource, int64, error)

	// Used returns aggregated usage for one project (projectID set) or every
	// project the caller owns (projectID nil), mirroring services/used.py.
	Used(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (*UsageStats, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

func (u *usecase) CreateFile(ctx context.Context, in CreateFileInput) (*File, error) {
	if !ValidType(in.FileType) {
		in.FileType = TypeFile
	}
	f, err := u.repo.CreateFile(ctx, in)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("file.project_not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return f, nil
}

func (u *usecase) GetFilePublic(ctx context.Context, uid uuid.UUID) (*File, error) {
	return mapFileErr(u.repo.GetFileByUUID(ctx, uid))
}

// ProjectOwner resolves the owner a module-file upload is stamped to. Maps a
// missing project to a 404 so the engine surfaces a clean error.
func (u *usecase) ProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	owner, err := u.repo.ProjectOwner(ctx, projectID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return uuid.Nil, httpx.NotFound("file.project_not_found", "project not found")
		}
		return uuid.Nil, httpx.Internal(err)
	}
	return owner, nil
}

func (u *usecase) GetFileForOwner(ctx context.Context, uid, ownerID uuid.UUID) (*File, error) {
	return mapFileErr(u.repo.GetFileByUUIDForOwner(ctx, uid, ownerID))
}

func (u *usecase) UpdateFile(ctx context.Context, uid, ownerID uuid.UUID, in UpdateFileInput) (*File, error) {
	if in.FileType != nil && !ValidType(*in.FileType) {
		return nil, httpx.BadRequest("file.invalid_filetype", "invalid filetype")
	}
	return mapFileErr(u.repo.UpdateFileByUUIDForOwner(ctx, uid, ownerID, in))
}

// DeleteFile returns the deleted row so the handler can unlink its blob from
// disk; the metadata row is already gone by the time this returns.
func (u *usecase) DeleteFile(ctx context.Context, uid, ownerID uuid.UUID) (*File, error) {
	return mapFileErr(u.repo.DeleteFileByUUIDForOwner(ctx, uid, ownerID))
}

func (u *usecase) ListFiles(ctx context.Context, f ListFileFilter) ([]File, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.ListFiles(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountFiles(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) CreateResource(ctx context.Context, in CreateResourceInput) (*Resource, error) {
	res, err := u.repo.CreateResource(ctx, in)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("resource.project_not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return res, nil
}

func (u *usecase) GetResource(ctx context.Context, id, ownerID uuid.UUID) (*Resource, error) {
	res, err := u.repo.GetResourceForOwner(ctx, id, ownerID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("resource.not_found", "resource not found")
		}
		return nil, httpx.Internal(err)
	}
	return res, nil
}

func (u *usecase) ListResources(ctx context.Context, f ListResourceFilter) ([]Resource, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.ListResources(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountResources(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) Used(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (*UsageStats, error) {
	if projectID != nil {
		owned, err := u.repo.IsProjectOwnedBy(ctx, *projectID, ownerID)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		if !owned {
			return nil, httpx.NotFound("used.project_not_found", "project not found")
		}
	}

	since := time.Now().Add(-UsageWindow)

	usedStorage, err := u.repo.SumFileSize(ctx, ownerID, projectID)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	latest, err := u.repo.LatestResourceByHostname(ctx, ownerID, projectID, since)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	var ram, cpu, netRX, netTX int64
	for _, res := range latest {
		ram += int64(res.RAM)
		cpu += int64(res.CPU)
		netRX += res.NetRX
		netTX += res.NetTX
	}

	chart, err := u.repo.ResourceHourly(ctx, ownerID, projectID, since)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	storageChart, err := u.repo.StorageHourly(ctx, ownerID, projectID, since)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	return &UsageStats{
		UsedStorage:  usedStorage,
		UsedRAM:      ram,
		UsedCPU:      cpu,
		UsedNetTX:    netTX,
		UsedNetRX:    netRX,
		Chart:        chart,
		StorageChart: storageChart,
	}, nil
}

// mapFileErr translates a not-found into a 404 and any other error into a 500.
func mapFileErr(f *File, err error) (*File, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("file.not_found", "file not found")
		}
		return nil, httpx.Internal(err)
	}
	return f, nil
}
