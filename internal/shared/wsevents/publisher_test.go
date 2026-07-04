package wsevents

import (
	"context"
	"encoding/json"
	"testing"
)

// TestBuildEnvelopeMatchesDjangoShape locks down the exact wire shape Django's
// ws_publish produces (core/apps/websocket/services/events.py:
// json.dumps({"room": ..., "data": data.model_dump()}) where
// data = WsEvents(event=event, data=schema)) — the standalone websocket
// service (unchanged) decodes this shape, so a drift here silently breaks
// real-time delivery to the constructor.
func TestBuildEnvelopeMatchesDjangoShape(t *testing.T) {
	body, err := buildEnvelope("project_1408", EventReleaseStatus, map[string]any{
		"project":    1408,
		"status":     "running",
		"version":    3,
		"service_id": "svc-abc",
	})
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if got["room"] != "project_1408" {
		t.Fatalf("expected room project_1408, got %v", got["room"])
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data to be an object, got %T", got["data"])
	}
	if data["event"] != "release_status" {
		t.Fatalf("expected event release_status, got %v", data["event"])
	}
	inner, ok := data["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested data object, got %T", data["data"])
	}
	if inner["status"] != "running" {
		t.Fatalf("expected nested status running, got %v", inner["status"])
	}
}

// TestProjectRoomFormatsIntAndString verifies room naming matches
// project.usecase.IssueCollabToken's fmt.Sprintf("project_%v", projectRef)
// exactly for both a Django legacy int pk and a fallback uuid string.
func TestProjectRoomFormatsIntAndString(t *testing.T) {
	if got := ProjectRoom(int32(1408)); got != "project_1408" {
		t.Fatalf("expected project_1408, got %q", got)
	}
	if got := ProjectRoom("9c1f6b2e-0000-0000-0000-000000000000"); got != "project_9c1f6b2e-0000-0000-0000-000000000000" {
		t.Fatalf("unexpected room for uuid ref: %q", got)
	}
}

// TestPublishNilSafe ensures a nil *RedisPublisher (the "Redis not
// configured" wiring in server.go) never panics — callers rely on this to
// unconditionally call Publish without a separate nil check.
func TestPublishNilSafe(t *testing.T) {
	var p *RedisPublisher
	p.Publish(context.Background(), "project_1", EventDebugStatus, map[string]any{"status": "running"})
	if err := p.Close(); err != nil {
		t.Fatalf("expected nil-safe Close, got %v", err)
	}
}
