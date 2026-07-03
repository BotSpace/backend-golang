package ws

import "encoding/json"

// Message is the envelope sent to clients. Keep it small and stable; clients
// depend on the JSON shape.
type Message struct {
	Type string `json:"type"`           // "message" | "system"
	From string `json:"from,omitempty"` // sender user id
	Room string `json:"room,omitempty"` // room name, if room-scoped
	Body string `json:"body,omitempty"` // text payload
}

// Inbound is the control protocol a client may send:
//
//	{"type":"join","room":"general"}
//	{"type":"leave","room":"general"}
//	{"type":"message","room":"general","body":"hi"}  // to a room
//	{"type":"message","body":"hi"}                    // to everyone
type Inbound struct {
	Type string `json:"type"`
	Room string `json:"room,omitempty"`
	Body string `json:"body,omitempty"`
}

func encode(m Message) []byte {
	b, _ := json.Marshal(m)
	return b
}

// decode parses an inbound control message. ok is false when data is not a
// valid JSON envelope (the caller then treats it as a plain text body).
func decode(data []byte) (Inbound, bool) {
	var in Inbound
	if err := json.Unmarshal(data, &in); err != nil || in.Type == "" {
		return Inbound{}, false
	}
	return in, true
}
