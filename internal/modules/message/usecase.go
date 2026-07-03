package message

import (
	"context"
	"errors"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Usecase exposes owner-scoped broadcast message operations.
type Usecase interface {
	Create(ctx context.Context, in CreateInput) (*Message, error)
	Get(ctx context.Context, id, ownerID uuid.UUID) (*Message, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Message, error)
	Delete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Message, int64, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

func (u *usecase) Create(ctx context.Context, in CreateInput) (*Message, error) {
	if in.MessageType == "" {
		in.MessageType = TypeText
	}
	if in.ButtonType == "" {
		in.ButtonType = ButtonReply
	}
	m, err := u.repo.Create(ctx, in)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("message.project_not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return m, nil
}

func (u *usecase) Get(ctx context.Context, id, ownerID uuid.UUID) (*Message, error) {
	m, err := u.repo.GetForOwner(ctx, id, ownerID)
	return mapErr(m, err)
}

func (u *usecase) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Message, error) {
	m, err := u.repo.Update(ctx, id, ownerID, in)
	return mapErr(m, err)
}

func (u *usecase) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	if err := u.repo.Delete(ctx, id, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("message.not_found", "message not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) List(ctx context.Context, f ListFilter) ([]Message, int64, error) {
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
	total, err := u.repo.Count(ctx, f.OwnerID, f.ProjectID)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// mapErr translates a not-found into a 404 and any other error into a 500.
func mapErr(m *Message, err error) (*Message, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("message.not_found", "message not found")
		}
		return nil, httpx.Internal(err)
	}
	return m, nil
}
