// Package redisq is the backend-side half of the engine→backend async write
// path: the engine-golang flow engine XADDs high-volume persistence events
// (bot-user upserts, debug/flow errors, token usage, …) to a Redis Stream
// instead of calling a synchronous internal HTTP API; this package consumes
// them with a consumer group so a backend restart never drops in-flight
// events (XREADGROUP re-delivers anything left unacked as "pending").
//
// Contract (matches worker-golang's internal/shared/redisq/stream.go, which
// already consumes Django's web→worker stream with this exact shape — reuse
// it verbatim so engine-golang only needs one XADD helper for every Go
// consumer it talks to):
//
//	XADD <stream> * payload '{"task":"<event.type>","args":{...}}'
//
// See docs/redis-stream-events.md for the full event-type registry this
// backend understands.
package redisq

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/botspace/backend/internal/shared/logger"
	"github.com/redis/go-redis/v9"
)

// Event is the envelope carried in the Redis Stream entry's "payload" field.
type Event struct {
	Task string          `json:"task"`
	Args json.RawMessage `json:"args"`
}

// Handler processes one event's Args. A non-nil error leaves the entry
// un-acked so it is redelivered (to this or another consumer) on the next
// pending-entries sweep — handlers must be idempotent.
type Handler func(ctx context.Context, args json.RawMessage) error

// Consumer reads a Redis Stream with a consumer group and dispatches each
// entry's "task" to a registered Handler.
type Consumer struct {
	rdb      *redis.Client
	stream   string
	group    string
	consumer string
	handlers map[string]Handler
	log      *logger.Logger
}

// NewConsumer dials Redis at addr (host:port) with password (empty for
// none). consumerName should be unique per replica (e.g. the pod name) so
// XREADGROUP's pending-entries list stays meaningful across restarts/scale;
// "backend-1" is fine for a single replica.
func NewConsumer(addr, password, stream, group, consumerName string, log *logger.Logger) *Consumer {
	return &Consumer{
		rdb: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
		}),
		stream:   stream,
		group:    group,
		consumer: consumerName,
		handlers: map[string]Handler{},
		log:      log,
	}
}

// Register binds a Handler to a "task" name. Call before Run.
func (c *Consumer) Register(task string, h Handler) { c.handlers[task] = h }

// Run blocks, consuming until ctx is cancelled. It first drains any
// previously-undelivered pending entries (id "0"), then reads new entries
// (id ">") in a loop.
func (c *Consumer) Run(ctx context.Context) error {
	if err := c.rdb.XGroupCreateMkStream(ctx, c.stream, c.group, "0").Err(); err != nil &&
		!errors.Is(err, redis.Nil) && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}

	c.consume(ctx, "0")
	for ctx.Err() == nil {
		c.consume(ctx, ">")
	}
	return ctx.Err()
}

// Close disconnects the underlying Redis client.
func (c *Consumer) Close() error { return c.rdb.Close() }

func (c *Consumer) consume(ctx context.Context, id string) {
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.consumer,
		Streams:  []string{c.stream, id},
		Count:    20,
		Block:    5 * time.Second,
	}).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) && ctx.Err() == nil {
			c.log.Warn("redisq: xreadgroup", logger.Err(err))
			time.Sleep(2 * time.Second)
		}
		return
	}
	for _, stream := range res {
		for _, msg := range stream.Messages {
			c.handle(ctx, msg)
		}
	}
}

func (c *Consumer) handle(ctx context.Context, msg redis.XMessage) {
	payload, _ := msg.Values["payload"].(string)
	var ev Event
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		c.log.Error("redisq: malformed payload", logger.String("id", msg.ID), logger.Err(err))
		c.rdb.XAck(ctx, c.stream, c.group, msg.ID) // unparseable — retrying never helps
		return
	}
	h, ok := c.handlers[ev.Task]
	if !ok {
		c.log.Error("redisq: no handler registered", logger.String("task", ev.Task))
		c.rdb.XAck(ctx, c.stream, c.group, msg.ID)
		return
	}
	if err := h(ctx, ev.Args); err != nil {
		// No ack — redelivered as a pending entry after a restart/rebalance.
		// No per-task retry cap: handlers must be idempotent and cheap to
		// retry rather than the consumer tracking attempt counts.
		c.log.Error("redisq: handler failed", logger.String("task", ev.Task), logger.Err(err))
		return
	}
	c.rdb.XAck(ctx, c.stream, c.group, msg.ID)
}
