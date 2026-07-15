package file

import (
	"context"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
// "ownership" is modelled directly as a projectID -> ownerID map, mirroring
// what the real SQL EXISTS/JOIN-on-projects checks enforce.
type fakeRepo struct {
	projects      map[uuid.UUID]uuid.UUID
	files         map[uuid.UUID]*File
	resources     map[uuid.UUID]*Resource
	hourly        []HourPoint
	storageHourly []StorageHourPoint
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		projects:  map[uuid.UUID]uuid.UUID{},
		files:     map[uuid.UUID]*File{},
		resources: map[uuid.UUID]*Resource{},
	}
}

func (r *fakeRepo) ownsProject(projectID, ownerID uuid.UUID) bool {
	owner, ok := r.projects[projectID]
	return ok && owner == ownerID
}

func (r *fakeRepo) CreateFile(_ context.Context, in CreateFileInput) (*File, error) {
	if !r.ownsProject(in.ProjectID, in.OwnerID) {
		return nil, database.ErrNotFound
	}
	f := &File{
		ID: uuid.New(), UUID: uuid.New(), ProjectID: in.ProjectID, OwnerID: in.OwnerID,
		FilePath: in.FilePath, OriginalName: in.OriginalName, FileSize: in.FileSize,
		FileType: in.FileType, ExpiresAt: in.ExpiresAt, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.files[f.UUID] = f
	cp := *f
	return &cp, nil
}

func (r *fakeRepo) GetFileByUUID(_ context.Context, uid uuid.UUID) (*File, error) {
	f, ok := r.files[uid]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *f
	return &cp, nil
}

func (r *fakeRepo) ProjectOwner(_ context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	owner, ok := r.projects[projectID]
	if !ok {
		return uuid.Nil, database.ErrNotFound
	}
	return owner, nil
}

func (r *fakeRepo) GetFileByUUIDForOwner(_ context.Context, uid, ownerID uuid.UUID) (*File, error) {
	f, ok := r.files[uid]
	if !ok || f.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	cp := *f
	return &cp, nil
}

func (r *fakeRepo) UpdateFileByUUIDForOwner(_ context.Context, uid, ownerID uuid.UUID, in UpdateFileInput) (*File, error) {
	f, ok := r.files[uid]
	if !ok || f.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	if in.FileType != nil {
		f.FileType = *in.FileType
	}
	cp := *f
	return &cp, nil
}

func (r *fakeRepo) DeleteFileByUUIDForOwner(_ context.Context, uid, ownerID uuid.UUID) (*File, error) {
	f, ok := r.files[uid]
	if !ok || f.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	delete(r.files, uid)
	cp := *f
	return &cp, nil
}

func (r *fakeRepo) ListFiles(_ context.Context, f ListFileFilter) ([]File, error) {
	var out []File
	for _, file := range r.files {
		if file.OwnerID != f.OwnerID {
			continue
		}
		if f.ProjectID != nil && file.ProjectID != *f.ProjectID {
			continue
		}
		if f.FileType != nil && file.FileType != *f.FileType {
			continue
		}
		out = append(out, *file)
	}
	return out, nil
}

func (r *fakeRepo) CountFiles(ctx context.Context, f ListFileFilter) (int64, error) {
	items, _ := r.ListFiles(ctx, f)
	return int64(len(items)), nil
}

func (r *fakeRepo) CreateResource(_ context.Context, in CreateResourceInput) (*Resource, error) {
	if !r.ownsProject(in.ProjectID, in.OwnerID) {
		return nil, database.ErrNotFound
	}
	res := &Resource{
		ID: uuid.New(), ProjectID: in.ProjectID, OwnerID: in.OwnerID, Hostname: in.Hostname,
		RAM: in.RAM, CPU: in.CPU, NetRX: in.NetRX, NetTX: in.NetTX,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.resources[res.ID] = res
	cp := *res
	return &cp, nil
}

func (r *fakeRepo) GetResourceForOwner(_ context.Context, id, ownerID uuid.UUID) (*Resource, error) {
	res, ok := r.resources[id]
	if !ok || res.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	cp := *res
	return &cp, nil
}

func (r *fakeRepo) ListResources(_ context.Context, f ListResourceFilter) ([]Resource, error) {
	var out []Resource
	for _, res := range r.resources {
		if res.OwnerID != f.OwnerID {
			continue
		}
		if f.ProjectID != nil && res.ProjectID != *f.ProjectID {
			continue
		}
		out = append(out, *res)
	}
	return out, nil
}

func (r *fakeRepo) CountResources(ctx context.Context, f ListResourceFilter) (int64, error) {
	items, _ := r.ListResources(ctx, f)
	return int64(len(items)), nil
}

func (r *fakeRepo) IsProjectOwnedBy(_ context.Context, projectID, ownerID uuid.UUID) (bool, error) {
	return r.ownsProject(projectID, ownerID), nil
}

func (r *fakeRepo) SumFileSize(_ context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error) {
	var total int64
	for _, f := range r.files {
		if f.OwnerID != ownerID {
			continue
		}
		if projectID != nil && f.ProjectID != *projectID {
			continue
		}
		total += f.FileSize
	}
	return total, nil
}

// LatestResourceByHostname emulates the SQL's DISTINCT ON (hostname) ... ORDER
// BY hostname, created_at DESC: exactly one row per hostname, the newest.
func (r *fakeRepo) LatestResourceByHostname(
	_ context.Context, ownerID uuid.UUID, projectID *uuid.UUID, since time.Time,
) ([]Resource, error) {
	latest := map[string]Resource{}
	for _, res := range r.resources {
		if res.OwnerID != ownerID || res.CreatedAt.Before(since) {
			continue
		}
		if projectID != nil && res.ProjectID != *projectID {
			continue
		}
		if cur, ok := latest[res.Hostname]; !ok || res.CreatedAt.After(cur.CreatedAt) {
			latest[res.Hostname] = *res
		}
	}
	out := make([]Resource, 0, len(latest))
	for _, res := range latest {
		out = append(out, res)
	}
	return out, nil
}

func (r *fakeRepo) ResourceHourly(context.Context, uuid.UUID, *uuid.UUID, time.Time) ([]HourPoint, error) {
	return r.hourly, nil
}

func (r *fakeRepo) StorageHourly(context.Context, uuid.UUID, *uuid.UUID, time.Time) ([]StorageHourPoint, error) {
	return r.storageHourly, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestCreateFileRejectsUnownedProject(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()

	owner := uuid.New()
	stranger := uuid.New()
	project := uuid.New()
	repo.projects[project] = owner

	if _, err := uc.CreateFile(ctx, CreateFileInput{
		OwnerID: stranger, ProjectID: project, FilePath: "p/f.bin", FileSize: 10,
	}); status(err) != 404 {
		t.Fatalf("expected 404 for a project the caller does not own, got %v", err)
	}

	f, err := uc.CreateFile(ctx, CreateFileInput{
		OwnerID: owner, ProjectID: project, FilePath: "p/f.bin", OriginalName: "f.bin", FileSize: 10,
	})
	if err != nil {
		t.Fatalf("create by the actual owner should succeed: %v", err)
	}
	if f.FileType != TypeFile {
		t.Fatalf("expected default filetype %q, got %q", TypeFile, f.FileType)
	}
}

func TestUpdateFileRejectsInvalidType(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()

	owner := uuid.New()
	project := uuid.New()
	repo.projects[project] = owner
	f, err := uc.CreateFile(ctx, CreateFileInput{OwnerID: owner, ProjectID: project, FilePath: "p/f.bin"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	bogus := "not-a-type"
	if _, err := uc.UpdateFile(ctx, f.UUID, owner, UpdateFileInput{FileType: &bogus}); status(err) != 400 {
		t.Fatalf("expected 400 for an invalid filetype, got %v", err)
	}

	photo := TypePhoto
	updated, err := uc.UpdateFile(ctx, f.UUID, owner, UpdateFileInput{FileType: &photo})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.FileType != TypePhoto {
		t.Fatalf("expected filetype %q, got %q", TypePhoto, updated.FileType)
	}
}

// TestUsedSumsLatestPerHostnameOnly asserts the "used" aggregation counts
// each reporting host once (its most recent sample), not every historical
// row — mirrors services/used.py's DISTINCT ON (hostname) queryset.
func TestUsedSumsLatestPerHostnameOnly(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()

	owner := uuid.New()
	project := uuid.New()
	repo.projects[project] = owner

	if _, err := uc.CreateFile(ctx, CreateFileInput{
		OwnerID: owner, ProjectID: project, FilePath: "p/a.bin", FileSize: 100,
	}); err != nil {
		t.Fatalf("create file: %v", err)
	}

	// Same hostname reports twice; only the newer sample should count.
	if _, err := uc.CreateResource(ctx, CreateResourceInput{
		OwnerID: owner, ProjectID: project, Hostname: "pod-1", RAM: 10, CPU: 5, NetRX: 1, NetTX: 1,
	}); err != nil {
		t.Fatalf("create resource: %v", err)
	}
	// Force the second sample to be strictly newer.
	for _, res := range repo.resources {
		res.CreatedAt = res.CreatedAt.Add(-time.Minute)
	}
	if _, err := uc.CreateResource(ctx, CreateResourceInput{
		OwnerID: owner, ProjectID: project, Hostname: "pod-1", RAM: 20, CPU: 15, NetRX: 2, NetTX: 2,
	}); err != nil {
		t.Fatalf("create resource: %v", err)
	}
	if _, err := uc.CreateResource(ctx, CreateResourceInput{
		OwnerID: owner, ProjectID: project, Hostname: "pod-2", RAM: 30, CPU: 25, NetRX: 3, NetTX: 3,
	}); err != nil {
		t.Fatalf("create resource: %v", err)
	}

	stats, err := uc.Used(ctx, owner, nil)
	if err != nil {
		t.Fatalf("used: %v", err)
	}
	if stats.UsedStorage != 100 {
		t.Fatalf("expected used_storage 100, got %d", stats.UsedStorage)
	}
	// pod-1 latest (20 ram / 15 cpu) + pod-2 (30 ram / 25 cpu), not the stale 10/5 sample.
	if stats.UsedRAM != 50 || stats.UsedCPU != 40 {
		t.Fatalf("expected ram=50 cpu=40, got ram=%d cpu=%d", stats.UsedRAM, stats.UsedCPU)
	}
	if stats.UsedNetRX != 5 || stats.UsedNetTX != 5 {
		t.Fatalf("expected net_rx=5 net_tx=5, got net_rx=%d net_tx=%d", stats.UsedNetRX, stats.UsedNetTX)
	}
}

func TestUsedRejectsUnownedProject(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()

	owner := uuid.New()
	stranger := uuid.New()
	project := uuid.New()
	repo.projects[project] = owner

	if _, err := uc.Used(ctx, stranger, &project); status(err) != 404 {
		t.Fatalf("expected 404 for a project the caller does not own, got %v", err)
	}
	if _, err := uc.Used(ctx, owner, &project); err != nil {
		t.Fatalf("used by the actual owner should succeed: %v", err)
	}
}
