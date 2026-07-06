package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

type fakeRepo struct {
	tenant any
	err    error
}

func (r *fakeRepo) ResolveProjectForOwner(context.Context, uuid.UUID, string) (any, error) {
	return r.tenant, r.err
}

type fakeMongo struct {
	users      []map[string]any
	totalUsers int64
	stats      map[any]chatStats
	messages   []map[string]any
	totalMsgs  int64
	err        error
}

func (m *fakeMongo) findAll(context.Context, string, any, int64) ([]map[string]any, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.users, m.totalUsers, nil
}

func (m *fakeMongo) chatStatsByChatID(context.Context, string, any) (map[any]chatStats, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.stats, nil
}

func (m *fakeMongo) findByChatID(context.Context, string, any, any, int64, int64) ([]map[string]any, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.messages, m.totalMsgs, nil
}

func TestListUsers_SortsAndPaginatesByLastMessage(t *testing.T) {
	uc := &usecase{
		repo: &fakeRepo{tenant: int64(42)},
		mongo: &fakeMongo{
			totalUsers: 3,
			users: []map[string]any{
				{"chat_id": int64(1), "name": "old"},
				{"chat_id": int64(2), "name": "new"},
				{"chat_id": int64(3), "name": "empty"},
			},
			stats: map[any]chatStats{
				int64(1): {LastMessage: map[string]any{"_id": "65a000000000000000000001"}, TotalMessages: 2},
				int64(2): {LastMessage: map[string]any{"_id": "65b000000000000000000001"}, UnreadCount: 1, TotalMessages: 3},
			},
		},
	}

	page, err := uc.ListUsers(context.Background(), uuid.New(), "42", 2, 0)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 2 {
		t.Fatalf("page = %#v, want total 3 and 2 items", page)
	}
	if got := page.Items[0].User["name"]; got != "new" {
		t.Fatalf("first user = %v, want new", got)
	}
	if page.Items[0].UnreadCount != 1 || page.Items[0].TotalMessages != 3 {
		t.Fatalf("stats not merged: %#v", page.Items[0])
	}
}

func TestListUsers_ProjectNotFound(t *testing.T) {
	uc := &usecase{repo: &fakeRepo{err: database.ErrNotFound}, mongo: &fakeMongo{}}

	_, err := uc.ListUsers(context.Background(), uuid.New(), "missing", 50, 0)
	ae, ok := httpx.AsAppError(err)
	if !ok || ae.Code != "project.not_found" {
		t.Fatalf("err = %v, want project.not_found AppError", err)
	}
}

func TestListMessages_ReversesPageOldestFirst(t *testing.T) {
	uc := &usecase{
		repo: &fakeRepo{tenant: "project-uuid"},
		mongo: &fakeMongo{
			totalMsgs: 2,
			messages: []map[string]any{
				{"text": "new"},
				{"text": "old"},
			},
		},
	}

	page, err := uc.ListMessages(context.Background(), uuid.New(), "project-uuid", 99, 50, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0]["text"] != "old" || page.Items[1]["text"] != "new" {
		t.Fatalf("messages = %#v, want oldest first", page.Items)
	}
}

func TestListMessages_MongoErrorIsInternal(t *testing.T) {
	uc := &usecase{repo: &fakeRepo{tenant: 1}, mongo: &fakeMongo{err: errors.New("mongo down")}}

	_, err := uc.ListMessages(context.Background(), uuid.New(), "1", 99, 50, 0)
	ae, ok := httpx.AsAppError(err)
	if !ok || ae.Code != "internal.error" {
		t.Fatalf("err = %v, want internal.error AppError", err)
	}
}
