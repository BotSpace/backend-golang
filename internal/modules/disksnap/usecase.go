// Package disksnap — celery snapshot_project_disk: reliz qilingan har loyihaning
// disk hajmi (fayllar + CMS) snapshot'ini yozadi (monitoring tarix charti).
package disksnap

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/mongox"
)

type Usecase struct {
	Repo  Repository
	Mongo *mongox.Client
	Log   *zap.Logger
}

func (u *Usecase) Run(ctx context.Context) {
	projects, err := u.Repo.ListReleasedProjectsForDisk(ctx)
	if err != nil {
		u.Log.Error("snapshot_project_disk: list", zap.Error(err))
		return
	}
	count := 0
	for _, project := range projects {
		projectID := uuidFromPG(project.ID)
		filesBytes, err := u.Repo.SumProjectFileBytes(ctx, project.ID)
		if err != nil {
			u.Log.Warn("snapshot: files bytes", zap.String("project", projectID), zap.Error(err))
			continue
		}

		cmsBytes, err := u.Mongo.TenantDataSize(ctx, "botspace_cms", mongoTenant(project))
		if err != nil {
			cmsBytes = 0 // db bo'lmasligi normal (CMS ishlatmagan loyiha)
		}
		err = u.Repo.InsertDiskSnapshot(ctx, project.ID, filesBytes, cmsBytes)
		if err != nil {
			u.Log.Warn("snapshot: insert", zap.String("project", projectID), zap.Error(err))
			continue
		}
		count++
	}
	u.Log.Info("snapshot_project_disk done", zap.Int("wrote", count))
}

func uuidFromPG(id uuid.UUID) string {
	return database.UUIDString(id)
}

func mongoTenant(project ReleasedProject) any {
	if project.LegacyID != nil {
		return int64(*project.LegacyID)
	}
	return uuidFromPG(project.ID)
}
