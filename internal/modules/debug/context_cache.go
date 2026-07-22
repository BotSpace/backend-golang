package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// chatsSetTTL/chatsSetCap mirror Django's DebugContextEventView constants
// (_CHATS_SET_TTL / _CHATS_CAP, core/apps/api/views/debug.py) — the
// per-project SET of chat_ids that have ever had a snapshot cached, so
// ListContext (GET /api/v1/debug/context/?project_id=) knows which
// per-chat keys to look up without an expensive Redis SCAN.
const (
	chatsSetTTL = 24 * time.Hour
	chatsSetCap = 50
	commandTTL  = 2 * time.Minute
)

// RedisContextCache implements ContextCache by SETting the latest variable
// snapshot per (project, chat) with a TTL, plus a per-project chat-id set —
// mirrors Django's core/apps/api/views/debug.py DebugContextEventView /
// DebugContextListView, which cache only the latest snapshot per chat in
// Redis (never a growing Postgres table). See docs/redis-stream-events.md
// "debug.context_event".
type RedisContextCache struct {
	rdb *redis.Client
}

// NewRedisContextCache builds a cache client against the same Redis
// instance the redisq consumer already uses (internal/shared/config
// Engine.RedisAddr/RedisPassword) — no separate connection pool needed
// beyond this one small client.
func NewRedisContextCache(addr, password string) *RedisContextCache {
	return &RedisContextCache{rdb: redis.NewClient(&redis.Options{Addr: addr, Password: password})}
}

func (c *RedisContextCache) Close() error { return c.rdb.Close() }

// contextSnapshotValue is the Redis-cached wire shape for one (project,
// chat) pair — mirrors Django's debug_ctx_{pk}_{chat_id} cache value
// ({"project", "chat_id", "variables", "ts"}), minus "project" (this
// backend's internal projects.id has no single Django-shaped scalar
// representation — callers that need a "project" field, e.g. the collab
// module's room naming, already resolve legacy_id/uuid themselves).
type contextSnapshotValue struct {
	ChatID    int64           `json:"chat_id"`
	Variables json.RawMessage `json:"variables"`
	TS        *int64          `json:"ts"`
}

func (c *RedisContextCache) SetContext(ctx context.Context, projectID uuid.UUID, chatID int64, variables json.RawMessage, ts *int64, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = contextEventTTL
	}
	body, err := json.Marshal(contextSnapshotValue{ChatID: chatID, Variables: variables, TS: ts})
	if err != nil {
		return fmt.Errorf("marshal context snapshot: %w", err)
	}
	key := contextCacheKey(projectID, chatID)
	if err := c.rdb.Set(ctx, key, body, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}
	if err := c.rememberChatID(ctx, projectID, chatID); err != nil {
		return err
	}
	return nil
}

// rememberChatID appends chatID to the project's chat-id set (capped,
// oldest dropped first) — a plain read-modify-write under Redis, same
// non-atomic round trip Django's cache.get/json.loads/cache.set does. This
// is a low-frequency, effectively single-writer-per-chat operation: a lost
// race just means a chat_id is briefly missing from the list, never
// corrupts it.
func (c *RedisContextCache) rememberChatID(ctx context.Context, projectID uuid.UUID, chatID int64) error {
	key := chatsSetKey(projectID)
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("redis get %s: %w", key, err)
	}
	var ids []int64
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &ids)
	}
	for _, id := range ids {
		if id == chatID {
			return nil // already remembered
		}
	}
	ids = append(ids, chatID)
	if len(ids) > chatsSetCap {
		ids = ids[len(ids)-chatsSetCap:]
	}
	body, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("marshal chat ids: %w", err)
	}
	if err := c.rdb.Set(ctx, key, body, chatsSetTTL).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}
	return nil
}

func (c *RedisContextCache) ListContext(ctx context.Context, projectID uuid.UUID) ([]ContextSnapshot, error) {
	raw, err := c.rdb.Get(ctx, chatsSetKey(projectID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("redis get chats set: %w", err)
	}
	var ids []int64
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("unmarshal chat ids: %w", err)
	}

	out := make([]ContextSnapshot, 0, len(ids))
	for _, id := range ids {
		snapRaw, err := c.rdb.Get(ctx, contextCacheKey(projectID, id)).Bytes()
		if err != nil {
			continue // TTL expired (or missing) — Django skips these too
		}
		var snap contextSnapshotValue
		if err := json.Unmarshal(snapRaw, &snap); err != nil {
			continue
		}
		out = append(out, ContextSnapshot{ChatID: snap.ChatID, Variables: snap.Variables, TS: snap.TS})
	}
	// Newest first, matching Django's `chats.sort(key=lambda s: s.get("ts") or 0, reverse=True)`.
	sort.SliceStable(out, func(i, j int) bool {
		var ti, tj int64
		if out[i].TS != nil {
			ti = *out[i].TS
		}
		if out[j].TS != nil {
			tj = *out[j].TS
		}
		return ti > tj
	})
	return out, nil
}

func (c *RedisContextCache) EnqueueNodeRun(ctx context.Context, projectID uuid.UUID, chatID int64, nodeID string) error {
	body, err := json.Marshal(struct {
		ChatID int64  `json:"chat_id"`
		NodeID string `json:"node_id"`
	}{ChatID: chatID, NodeID: nodeID})
	if err != nil {
		return fmt.Errorf("marshal debug node command: %w", err)
	}
	key := debugCommandKey(projectID)
	pipe := c.rdb.TxPipeline()
	pipe.RPush(ctx, key, body)
	pipe.Expire(ctx, key, commandTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("enqueue debug node command: %w", err)
	}
	return nil
}

func contextCacheKey(projectID uuid.UUID, chatID int64) string {
	return fmt.Sprintf("debug:ctx:%s:%d", projectID, chatID)
}

func chatsSetKey(projectID uuid.UUID) string {
	return fmt.Sprintf("debug:ctx:chats:%s", projectID)
}

func debugCommandKey(projectID uuid.UUID) string {
	return fmt.Sprintf("engine:debug:commands:%s", projectID)
}
