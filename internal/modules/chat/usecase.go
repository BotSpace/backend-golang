package chat

import (
	"context"
	"errors"
	"sort"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

const (
	usersCollection = "users"
	chatCollection  = "chat"

	// maxUsersScan caps the "fetch every user" step of ListUsers — Django's
	// get_users_with_messages() has no cap (users_repo.find(limit=0)), but
	// that collection is shared across every project in botspace_cms, so an
	// unbounded scan is not something to expose over HTTP unauthenticated by
	// row count. 5000 comfortably covers any real bot's audience while
	// keeping one request bounded.
	maxUsersScan = 5000
)

// Usecase is the owner-scoped read side agentv3 and the constructor's CRM
// tab call.
type Usecase interface {
	ListUsers(ctx context.Context, ownerID uuid.UUID, rawProjectID string, limit, skip int64) (UsersPage, error)
	ListMessages(ctx context.Context, ownerID uuid.UUID, rawProjectID string, chatID int64, limit, skip int64) (MessagesPage, error)
}

type usecase struct {
	repo  Repository
	mongo mongoReader
}

func NewUsecase(repo Repository, mongo *MongoDB) Usecase {
	return &usecase{repo: repo, mongo: mongo}
}

type mongoReader interface {
	findAll(ctx context.Context, coll string, tenant any, maxRows int64) ([]map[string]any, int64, error)
	chatStatsByChatID(ctx context.Context, coll string, tenant any) (map[any]chatStats, error)
	findByChatID(ctx context.Context, coll string, tenant, chatID any, limit, skip int64) ([]map[string]any, int64, error)
}

// ListUsers ports ChatViewSet.get_users_with_messages: every "users"
// document for the project, merged with per-chat_id stats from the "chat"
// collection (last message, unread/total counts), sorted by last message
// time (newest first, users without messages last), then paginated.
func (u *usecase) ListUsers(ctx context.Context, ownerID uuid.UUID, rawProjectID string, limit, skip int64) (UsersPage, error) {
	tenant, err := u.repo.ResolveProjectForOwner(ctx, ownerID, rawProjectID)
	if err != nil {
		return UsersPage{}, mapProjectErr(err)
	}

	users, totalUsers, err := u.mongo.findAll(ctx, usersCollection, tenant, maxUsersScan)
	if err != nil {
		return UsersPage{}, httpx.Internal(err)
	}
	stats, err := u.mongo.chatStatsByChatID(ctx, chatCollection, tenant)
	if err != nil {
		return UsersPage{}, httpx.Internal(err)
	}

	items := make([]UserWithStats, 0, len(users))
	lastMsgID := make([]string, 0, len(users)) // parallel to items, for the sort below
	for _, user := range users {
		chatID := user["chat_id"]
		st, ok := stats[normalizeValue(chatID)]
		row := UserWithStats{User: user}
		sortKey := ""
		if ok {
			row.LastMessage = st.LastMessage
			row.UnreadCount = st.UnreadCount
			row.TotalMessages = st.TotalMessages
			if id, ok := st.LastMessage["_id"].(string); ok {
				sortKey = id
			}
		}
		items = append(items, row)
		lastMsgID = append(lastMsgID, sortKey)
	}

	// Sort by last message time descending; Mongo ObjectID hex strings sort
	// chronologically byte-wise (their first 4 bytes are a Unix timestamp),
	// matching Django's `x.get("last_message_time") or ""` string sort.
	// Users without any message (sortKey == "") sort last, same as Django.
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return lastMsgID[idx[a]] > lastMsgID[idx[b]]
	})
	sorted := make([]UserWithStats, len(items))
	for i, j := range idx {
		sorted[i] = items[j]
	}

	page := paginate(sorted, limit, skip)
	return UsersPage{Items: page, Total: totalUsers}, nil
}

// ListMessages ports ChatViewSet.get_user_messages: every "chat" document
// for one chat_id, oldest-first (Django fetches newest-first then
// .reverse()s the page).
func (u *usecase) ListMessages(ctx context.Context, ownerID uuid.UUID, rawProjectID string, chatID int64, limit, skip int64) (MessagesPage, error) {
	tenant, err := u.repo.ResolveProjectForOwner(ctx, ownerID, rawProjectID)
	if err != nil {
		return MessagesPage{}, mapProjectErr(err)
	}
	items, total, err := u.mongo.findByChatID(ctx, chatCollection, tenant, chatID, limit, skip)
	if err != nil {
		return MessagesPage{}, httpx.Internal(err)
	}
	// reverse in place: Mongo returned newest-first (sort _id desc), Django's
	// API contract is oldest-first within the returned page.
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return MessagesPage{Items: items, Total: total}, nil
}

// paginate applies Django's `result[skip:skip+limit]` slicing (limit<=0
// means "no limit", matching `if limit > 0 else result[skip:]`).
func paginate(items []UserWithStats, limit, skip int64) []UserWithStats {
	if skip < 0 {
		skip = 0
	}
	if skip >= int64(len(items)) {
		return []UserWithStats{}
	}
	end := int64(len(items))
	if limit > 0 && skip+limit < end {
		end = skip + limit
	}
	return items[skip:end]
}

func mapProjectErr(err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("project.not_found", "project not found")
	}
	return httpx.Internal(err)
}
