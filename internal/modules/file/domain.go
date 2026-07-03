// Package file ports Django's file/resource domain: uploaded file metadata
// (FileModel), periodic host resource metrics (ResourceModel), and
// project disk-usage snapshots (StorageResourceModel), plus the aggregated
// "used" read that combines them (services/used.py). The blob itself is
// stored on local disk under APP_MEDIA_DIR; only metadata lives in Postgres.
package file

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// File types recognised for an upload (mirrors FileTypeEnum).
const (
	TypeFile  = "file"
	TypePhoto = "photo"
	TypeVideo = "video"
	TypeAudio = "audio"
)

// ValidType reports whether t is one of the recognised file types.
func ValidType(t string) bool {
	switch t {
	case TypeFile, TypePhoto, TypeVideo, TypeAudio:
		return true
	}
	return false
}

// UsageWindow is how far back "used" totals and chart points look (mirrors
// services/used.py's fixed 24h window).
const UsageWindow = 24 * time.Hour

// File is an uploaded blob's metadata. UUID is the public lookup key
// (Django's lookup_field="uuid", AllowAny on retrieve); ID is the internal
// PK. FilePath is relative to APP_MEDIA_DIR and is never exposed to clients.
type File struct {
	ID           uuid.UUID
	UUID         uuid.UUID
	ProjectID    uuid.UUID
	OwnerID      uuid.UUID // uploader (FileModel.user)
	FilePath     string
	OriginalName string
	FileSize     int64
	FileType     string
	ExpiresAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateFileInput is the owner-scoped create payload. OwnerID both stamps
// the uploader and proves ProjectID belongs to the caller.
type CreateFileInput struct {
	OwnerID      uuid.UUID
	ProjectID    uuid.UUID
	FilePath     string
	OriginalName string
	FileSize     int64
	FileType     string
	ExpiresAt    *time.Time
}

// UpdateFileInput carries optional fields; nil pointers are left unchanged.
type UpdateFileInput struct {
	FileType *string
}

// ListFileFilter narrows and paginates an owner's file listing. ProjectID
// and FileType are optional; nil means "no filter" on that dimension.
type ListFileFilter struct {
	OwnerID   uuid.UUID
	ProjectID *uuid.UUID
	FileType  *string
	Limit     int32
	Offset    int32
}

// Resource is one periodic host-metrics report for a project's running bot
// (ResourceModel). Hostname disambiguates multiple pods/replicas.
type Resource struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	Hostname  string
	RAM       int32
	CPU       int32
	NetRX     int64
	NetTX     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateResourceInput is the owner-scoped create payload.
type CreateResourceInput struct {
	OwnerID   uuid.UUID
	ProjectID uuid.UUID
	Hostname  string
	RAM       int32
	CPU       int32
	NetRX     int64
	NetTX     int64
}

// ListResourceFilter narrows and paginates an owner's resource listing.
type ListResourceFilter struct {
	OwnerID   uuid.UUID
	ProjectID *uuid.UUID
	Limit     int32
	Offset    int32
}

// HourPoint is one point of the RAM/CPU/network hourly usage chart.
type HourPoint struct {
	Hour  time.Time
	RAM   float64
	CPU   float64
	NetRX int64
	NetTX int64
}

// StorageHourPoint is one point of the storage hourly usage chart.
type StorageHourPoint struct {
	Hour    time.Time
	Storage int64
}

// UsageStats is the aggregated usage snapshot returned by /used (mirrors
// UsedStatSerializer). Scope is either one project or every project the
// caller owns, selected by whether the request carries a project id.
type UsageStats struct {
	UsedStorage  int64
	UsedRAM      int64
	UsedCPU      int64
	UsedNetTX    int64
	UsedNetRX    int64
	Chart        []HourPoint
	StorageChart []StorageHourPoint
}

// Repository is the persistence port; every operation is scoped to the
// caller's own projects (via projects.owner_id) so a user can never read or
// write another user's files/resources. GetFileByUUID is the one exception:
// it is intentionally public, mirroring Django's AllowAny retrieve.
type Repository interface {
	CreateFile(ctx context.Context, in CreateFileInput) (*File, error)
	GetFileByUUID(ctx context.Context, uid uuid.UUID) (*File, error)
	GetFileByUUIDForOwner(ctx context.Context, uid, ownerID uuid.UUID) (*File, error)
	UpdateFileByUUIDForOwner(ctx context.Context, uid, ownerID uuid.UUID, in UpdateFileInput) (*File, error)
	DeleteFileByUUIDForOwner(ctx context.Context, uid, ownerID uuid.UUID) (*File, error)
	ListFiles(ctx context.Context, f ListFileFilter) ([]File, error)
	CountFiles(ctx context.Context, f ListFileFilter) (int64, error)

	CreateResource(ctx context.Context, in CreateResourceInput) (*Resource, error)
	GetResourceForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Resource, error)
	ListResources(ctx context.Context, f ListResourceFilter) ([]Resource, error)
	CountResources(ctx context.Context, f ListResourceFilter) (int64, error)

	// IsProjectOwnedBy reports whether projectID exists and belongs to
	// ownerID, so the "used" usecase can 404 instead of returning zeros for a
	// project the caller cannot see.
	IsProjectOwnedBy(ctx context.Context, projectID, ownerID uuid.UUID) (bool, error)
	SumFileSize(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error)
	LatestResourceByHostname(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID, since time.Time) ([]Resource, error)
	ResourceHourly(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID, since time.Time) ([]HourPoint, error)
	StorageHourly(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID, since time.Time) ([]StorageHourPoint, error)
}
