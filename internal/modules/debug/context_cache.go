package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RedisContextCache implements ContextCache by SETting the latest variable
// snapshot per (project, chat) with a TTL — mirrors Django's
// core/apps/api/views/debug.py DebugContextEventView, which cached only the
// latest snapshot in Redis (never a growing Postgres table). See
// docs/redis-stream-events.md "debug.context_event".
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

func (c *RedisContextCache) SetContext(ctx context.Context, projectID uuid.UUID, chatID int64, variables json.RawMessage, ttl time.Duration) error {
	key := contextCacheKey(projectID, chatID)
	if err := c.rdb.Set(ctx, key, []byte(variables), ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}
	return nil
}

func (c *RedisContextCache) Close() error { return c.rdb.Close() }

func contextCacheKey(projectID uuid.UUID, chatID int64) string {
	return fmt.Sprintf("debug:ctx:%s:%d", projectID, chatID)
}
