// Package wsevents publishes real-time domain events to the constructor
// through the standalone `websocket` service (JscorpTech/websocket — a
// SEPARATE deployment this backend does not own). That service subscribes to
// a single Redis Pub/Sub channel ("websocket", hardcoded in its
// internal/config.Config — see its cmd/main.go) and relays whatever JSON is
// published there verbatim into the named room's connected clients (its
// internal/handlers/redis.go Watch -> hub.Broadcast).
//
// This mirrors Django's core/apps/websocket/services/events.py ws_publish
// EXACTLY — same channel name, same {"room","data":{"event","data"}}
// envelope — so the unchanged websocket service keeps broadcasting to the
// constructor without any change on its side.
//
// Room naming is where this matters: the constructor actually holds TWO WS
// connections to that service — a collab-client (binary flow-edit ops only,
// joins "project_<ref>" via its collab-token's project_id claim) and a
// ws-hub (decodes the JSON domain events this package publishes:
// release_status, debug_status, node_execution, context, error). The ws-hub
// authenticates with a plain access token that carries NO project_id claim,
// so the websocket service always assigns it to "user_<user_id>" (see
// archive/websocket/cmd/main.go's room-assignment logic) regardless of which
// project is open. Publishing domain events to "project_<ref>" (as an
// earlier version of this file did, matching only the collab-client's room)
// meant the ws-hub — the only connection that reads JSON — never received
// them. Use UserRoom (keyed by the project's OWNER, mirroring Django's old
// "user_<service.project.owner.id>") for every JSON domain event; ProjectRoom
// remains correct only for anything the collab-client itself needs.
package wsevents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/botspace/backend/internal/shared/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// channelName is the fixed Redis Pub/Sub channel the websocket service
// subscribes to. Not configurable on that side (JscorpTech/websocket
// internal/config.Config.ChannelName is hardcoded "websocket"), so this must
// match exactly — do not change without changing the other repo too.
const channelName = "websocket"

// Event type constants — mirror core/domain/ws.py's EventTypes enum values
// exactly (the constructor keys off these strings).
const (
	EventReleaseStatus = "release_status"
	EventDebugStatus   = "debug_status"
	EventNodeExecution = "node_execution"
	EventContext       = "context"
	EventError         = "error"
	// EventFlowChanged tells the constructor that the project's flow was updated
	// out-of-band (by the AI agent) so it can reload — see project.Update.
	EventFlowChanged = "flow_changed"
)

// ProjectRoom formats the "project_<ref>" room name, matching
// project.usecase.IssueCollabToken's `fmt.Sprintf("project_%v", projectRef)`
// exactly — ref is either an int32 (Django legacy pk) or a string (this
// backend's own uuid, or the engine's raw project_id reference forwarded
// as-is). Only the collab-token's binary-op WS connection joins this room —
// domain JSON events (release/debug status, node execution, context, error)
// must use UserRoom instead, see below.
func ProjectRoom(ref any) string {
	return fmt.Sprintf("project_%v", ref)
}

// UserRoom formats the "user_<owner-id>" room name, matching Django's
// _send_ws_notification (core/apps/api/management/commands/watch_k8s_events.py
// published to "user_<service.project.owner.id>"). The constructor's ws-hub
// connection (the one that decodes JSON domain events — release_status,
// debug_status, node_execution, context, error) authenticates with a plain
// access token that has NO project_id claim, so the standalone websocket
// service (archive/websocket/cmd/main.go) always assigns it to
// "user_<user_id>", never a "project_<ref>" room. Every JSON domain event
// MUST therefore be published here (keyed by the PROJECT'S OWNER, since a
// project's constructor session belongs to its owner) or the ws-hub never
// sees it — see internal/modules/release/ws_events.go and
// internal/server/server.go's startEventConsumer.
func UserRoom(ownerID uuid.UUID) string {
	return "user_" + ownerID.String()
}

// Publisher publishes one event into a room. Implementations must be
// nil-receiver-safe (see RedisPublisher.Publish) so callers can wire a
// possibly-nil instance without a separate nil check at every call site.
type Publisher interface {
	Publish(ctx context.Context, room, event string, data any)
}

// RedisPublisher is the production Publisher, backed by a redis.Client
// against the same engine Redis this backend's redisq consumer already uses
// (APP_ENGINE_REDIS_* — see internal/shared/config EngineConfig.RedisAddr).
type RedisPublisher struct {
	rdb *redis.Client
	log *logger.Logger
}

// NewRedisPublisher builds a publisher. addr must be non-empty (callers
// should only construct this when EngineConfig.RedisAddr() is set — see
// server.go's startEventConsumer for the same convention); log may be nil.
func NewRedisPublisher(addr, password string, log *logger.Logger) *RedisPublisher {
	return &RedisPublisher{
		rdb: redis.NewClient(&redis.Options{Addr: addr, Password: password}),
		log: log,
	}
}

// Publish is fire-and-forget and best-effort — mirrors Django's
// _send_ws_notification, which wraps the whole publish in a bare try/except
// so a WS hiccup never fails the request/event that triggered it. Safe to
// call on a nil *RedisPublisher (a nil Publisher interface wired at startup
// when Redis isn't configured) — it just no-ops.
func (p *RedisPublisher) Publish(ctx context.Context, room, event string, data any) {
	if p == nil || p.rdb == nil {
		return
	}
	body, err := buildEnvelope(room, event, data)
	if err != nil {
		p.warn("wsevents: marshal event failed", err, room, event)
		return
	}
	res := p.rdb.Publish(ctx, channelName, body)
	if err := res.Err(); err != nil {
		p.warn("wsevents: publish failed", err, room, event)
		return
	}
	// receivers is the number of Redis subscribers that got this message — i.e.
	// how many `websocket` service replicas are listening. 0 means the event was
	// published into the void (service down / not subscribed / wrong Redis), the
	// single most useful signal when debugging "events don't reach the client".
	if p.log != nil {
		p.log.Info("wsevents: published",
			logger.String("channel", channelName),
			logger.String("room", room),
			logger.String("event", event),
			logger.Int64("receivers", res.Val()))
	}
}

// buildEnvelope encodes the {"room","data":{"event","data"}} wire shape —
// split out from Publish so it can be unit-tested byte-for-byte against
// Django's json.dumps({"room": ..., "data": {"event": ..., "data": ...}})
// output without a real Redis connection.
func buildEnvelope(room, event string, data any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"room": room,
		"data": map[string]any{
			"event": event,
			"data":  data,
		},
	})
}

// Close releases the underlying Redis connection pool.
func (p *RedisPublisher) Close() error {
	if p == nil || p.rdb == nil {
		return nil
	}
	return p.rdb.Close()
}

func (p *RedisPublisher) warn(msg string, err error, room, event string) {
	if p.log == nil {
		return
	}
	p.log.Warn(msg, logger.Err(err), logger.String("room", room), logger.String("event", event))
}
