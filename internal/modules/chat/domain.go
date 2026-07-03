// Package chat ports the read-only side of Django's ChatViewSet
// (core/apps/api/views/chat.py): the constructor's CRM tab and agentv3's
// list_chat_users/user_chat_history tools list a project's Telegram end
// users and their message history. Documents live in the shared Mongo
// botspace_cms database (system "users"/"chat" collections written by
// engine-golang), scoped by the same `_project_id` tenant field the cms
// module uses — see mongo.go for why this package has its own small Mongo
// client instead of importing internal/modules/cms (PORTING_GUIDE.md rule
// #4: don't cross-import a sibling module, scope in SQL instead).
//
// Write-side actions (mark-read, send-message, chat stats) are NOT ported —
// out of scope per the task that added this module; only the two GET reads
// agent/src/agent/integrations/backend.py calls.
package chat

import (
	"context"

	"github.com/google/uuid"
)

// Repository resolves an owner-scoped project reference (Django int legacy
// pk or UUID) into the Mongo tenant key documents are scoped by.
type Repository interface {
	// ResolveProjectForOwner verifies rawProjectID belongs to ownerID and
	// returns the Mongo tenant value to filter `_project_id` by: the Django
	// legacy int pk when the project was imported from Django, else the
	// project's own UUID string (mirrors cms.usecase.resolveMongoTenant).
	// Returns database.ErrNotFound when rawProjectID doesn't resolve to a
	// project owned by ownerID (covers "wrong owner" and "no such project"
	// alike, same as Django's Http404).
	ResolveProjectForOwner(ctx context.Context, ownerID uuid.UUID, rawProjectID string) (tenant any, err error)
}

// UserWithStats is one row of the users/{project_id}/ listing: a raw "users"
// collection document (whatever fields the flow's Save-User node wrote) plus
// aggregated stats from the "chat" collection.
type UserWithStats struct {
	User          map[string]any `json:"user"`
	LastMessage   map[string]any `json:"last_message"`
	UnreadCount   int64          `json:"unread_count"`
	TotalMessages int64          `json:"total_messages"`
}

// UsersPage is the get_users_with_messages() result: paginated, sorted by
// last message time (newest first), mirroring the Python list slicing.
type UsersPage struct {
	Items []UserWithStats
	Total int64
}

// MessagesPage is the get_user_messages() result for one chat_id, oldest
// first (mirrors Django's messages.reverse()).
type MessagesPage struct {
	Items []map[string]any
	Total int64
}
