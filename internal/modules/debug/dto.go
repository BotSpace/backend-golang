package debug

import "time"

// ErrorEventResponse is the wire shape for GET /api/v1/debug/errors/,
// matching the spec agentv3's backend.py integration expects:
// {"errors": [{node_id, node_type, message, detail, chat_id, ts}]}.
type ErrorEventResponse struct {
	NodeID   string     `json:"node_id"`
	NodeType string     `json:"node_type"`
	Message  string     `json:"message"`
	Detail   string     `json:"detail"`
	ChatID   int64      `json:"chat_id"`
	TS       *time.Time `json:"ts"`
}

type ListErrorsResponse struct {
	Errors []ErrorEventResponse `json:"errors"`
}

func toErrorsResponse(items []ErrorEvent) ListErrorsResponse {
	out := make([]ErrorEventResponse, len(items))
	for i, e := range items {
		ts := e.EventTS
		if ts == nil {
			t := e.CreatedAt
			ts = &t
		}
		out[i] = ErrorEventResponse{
			NodeID: e.NodeID, NodeType: e.NodeType, Message: e.Message,
			Detail: e.Detail, ChatID: e.ChatID, TS: ts,
		}
	}
	return ListErrorsResponse{Errors: out}
}
