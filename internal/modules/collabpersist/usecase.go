// Package collabpersist persists constructor collaboration ops into
// backend-golang's projects.flow JSONB column.
package collabpersist

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vmihailenco/msgpack/v5"
	"go.uber.org/zap"
)

const channelName = "collab_ops"

type Usecase struct {
	Repo  Repository
	Redis *redis.Client
	Log   *zap.Logger
}

type payload struct {
	ProjectID string `json:"project_id"`
	Data      string `json:"data"`
}

type flowDoc struct {
	Nodes []any `json:"nodes"`
	Edges []any `json:"edges"`
	Extra map[string]any
}

func (u *Usecase) Run(ctx context.Context) {
	if u.Repo == nil || u.Redis == nil {
		u.Log.Warn("collab_persist disabled: missing repository or redis")
		return
	}

	pubsub := u.Redis.Subscribe(ctx, channelName)
	defer func() { _ = pubsub.Close() }()

	if _, err := pubsub.Receive(ctx); err != nil {
		u.Log.Error("collab_persist subscribe failed", zap.Error(err))
		return
	}
	u.Log.Info("collab_persist started", zap.String("channel", channelName))

	ch := pubsub.Channel(redis.WithChannelSize(1000))
	for {
		select {
		case <-ctx.Done():
			u.Log.Info("collab_persist stopped")
			return
		case msg, ok := <-ch:
			if !ok {
				u.Log.Warn("collab_persist channel closed")
				return
			}
			if err := u.handleMessage(ctx, msg.Payload); err != nil {
				u.Log.Warn("collab_persist message skipped", zap.Error(err))
			}
		}
	}
}

func (u *Usecase) handleMessage(ctx context.Context, raw string) error {
	var p payload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if p.ProjectID == "" || p.Data == "" {
		return fmt.Errorf("payload missing project_id or data")
	}

	op, err := decodeOp(p.Data)
	if err != nil {
		return err
	}

	projectID, changed, err := u.Repo.PersistOp(ctx, p.ProjectID, op)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	u.Log.Debug("collab_persist saved", zap.String("project_id", projectID), zap.String("op", opType(op)))
	return nil
}

func decodeOp(encoded string) (map[string]any, error) {
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode op: %w", err)
	}
	r := flate.NewReader(bytes.NewReader(compressed))
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("inflate op: %w", err)
	}
	var op map[string]any
	if err := msgpack.Unmarshal(body, &op); err != nil {
		return nil, fmt.Errorf("msgpack decode op: %w", err)
	}
	return op, nil
}

func applyOp(raw json.RawMessage, op map[string]any) ([]byte, bool, error) {
	flow, err := parseFlow(raw)
	if err != nil {
		return nil, false, err
	}

	before, _ := json.Marshal(flow)
	apply(flow, op)
	after, err := json.Marshal(flow)
	if err != nil {
		return nil, false, fmt.Errorf("encode flow: %w", err)
	}
	return after, !bytes.Equal(before, after), nil
}

func parseFlow(raw json.RawMessage) (*flowDoc, error) {
	out := &flowDoc{Nodes: []any{}, Edges: []any{}, Extra: map[string]any{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil && obj != nil {
		for k, v := range obj {
			switch k {
			case "nodes":
				out.Nodes = asSlice(v)
			case "edges":
				out.Edges = asSlice(v)
			default:
				out.Extra[k] = v
			}
		}
		return out, nil
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return out, nil
	}
	return nil, fmt.Errorf("flow is neither object nor array")
}

func (f *flowDoc) MarshalJSON() ([]byte, error) {
	obj := make(map[string]any, len(f.Extra)+2)
	for k, v := range f.Extra {
		obj[k] = v
	}
	obj["nodes"] = f.Nodes
	obj["edges"] = f.Edges
	return json.Marshal(obj)
}

func apply(flow *flowDoc, op map[string]any) {
	switch opType(op) {
	case "nu":
		id, _ := op["id"].(string)
		for _, node := range flow.Nodes {
			if m := asMap(node); nodeID(m) == id {
				m["data"] = op["data"]
				return
			}
		}
	case "nm":
		id, _ := op["id"].(string)
		pos := op["position"]
		for _, node := range flow.Nodes {
			if m := asMap(node); nodeID(m) == id {
				m["position"] = pos
				m["positionAbsolute"] = pos
				return
			}
		}
	case "na":
		node := asMap(op["node"])
		id := nodeID(node)
		if id == "" || hasID(flow.Nodes, id) {
			return
		}
		flow.Nodes = append(flow.Nodes, node)
	case "nd":
		id, _ := op["id"].(string)
		flow.Nodes = filter(flow.Nodes, func(item any) bool { return nodeID(asMap(item)) != id })
		flow.Edges = filter(flow.Edges, func(item any) bool {
			edge := asMap(item)
			return stringField(edge, "source") != id && stringField(edge, "target") != id
		})
	case "ea":
		edge := asMap(op["edge"])
		id := nodeID(edge)
		if id == "" || hasID(flow.Edges, id) {
			return
		}
		flow.Edges = append(flow.Edges, edge)
	case "ed":
		id, _ := op["id"].(string)
		flow.Edges = filter(flow.Edges, func(item any) bool { return nodeID(asMap(item)) != id })
	}
}

func opType(op map[string]any) string {
	t, _ := op["t"].(string)
	return t
}

func asSlice(v any) []any {
	if items, ok := v.([]any); ok {
		return items
	}
	return []any{}
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func nodeID(m map[string]any) string {
	return stringField(m, "id")
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func hasID(items []any, id string) bool {
	for _, item := range items {
		if nodeID(asMap(item)) == id {
			return true
		}
	}
	return false
}

func filter(items []any, keep func(any) bool) []any {
	out := items[:0]
	for _, item := range items {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

func DefaultFlushInterval() time.Duration { return 400 * time.Millisecond }
