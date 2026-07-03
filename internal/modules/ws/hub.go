// Package ws provides an authenticated WebSocket endpoint backed by a single
// broadcast hub. The hub owns all connection state and runs in one goroutine,
// so client/room maps need no locks — every mutation flows through Run's
// channels.
package ws

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second    // time allowed to write a message
	pongWait       = 60 * time.Second    // time allowed to read the next pong
	pingPeriod     = (pongWait * 9) / 10 // ping interval; must be < pongWait
	maxMessageSize = 4096                // max inbound message bytes
	sendBuffer     = 16                  // per-client outbound queue depth
)

// Hub fans messages out to connected clients. It supports three target scopes:
// everyone (Broadcast), a single user's connections (SendToUser), and named
// rooms that any number of users can join (BroadcastToRoom / JoinUser).
// Construct with NewHub and drive with Run.
type Hub struct {
	register   chan *Client
	unregister chan *Client
	broadcast  chan outbound
	roomCmd    chan roomCmd
	clients    map[*Client]struct{}
	byUser     map[string]map[*Client]struct{}
	rooms      map[string]map[*Client]struct{}
}

type outbound struct {
	payload []byte
	userID  string // if set, only this user's clients
	room    string // if set, only this room's members
}

// roomCmd joins/leaves either a single client (from its read pump) or all of a
// user's clients (server-side, via JoinUser/LeaveUser).
type roomCmd struct {
	client *Client
	userID string
	room   string
	join   bool
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan outbound, 256),
		roomCmd:    make(chan roomCmd, 64),
		clients:    make(map[*Client]struct{}),
		byUser:     make(map[string]map[*Client]struct{}),
		rooms:      make(map[string]map[*Client]struct{}),
	}
}

// Run processes hub events until ctx is cancelled, then closes every client.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				close(c.send)
			}
			h.clients = map[*Client]struct{}{}
			h.byUser = map[string]map[*Client]struct{}{}
			h.rooms = map[string]map[*Client]struct{}{}
			return

		case c := <-h.register:
			h.clients[c] = struct{}{}
			addTo(h.byUser, c.userID, c)

		case c := <-h.unregister:
			h.remove(c)

		case cmd := <-h.roomCmd:
			h.handleRoom(cmd)

		case msg := <-h.broadcast:
			targets := h.clients
			switch {
			case msg.room != "":
				targets = h.rooms[msg.room]
			case msg.userID != "":
				targets = h.byUser[msg.userID]
			}
			for c := range targets {
				select {
				case c.send <- msg.payload:
				default:
					// Slow client: drop it rather than block the hub.
					h.remove(c)
				}
			}
		}
	}
}

func (h *Hub) handleRoom(cmd roomCmd) {
	var targets []*Client
	switch {
	case cmd.client != nil:
		targets = []*Client{cmd.client}
	case cmd.userID != "":
		for c := range h.byUser[cmd.userID] {
			targets = append(targets, c)
		}
	}
	for _, c := range targets {
		if cmd.join {
			addTo(h.rooms, cmd.room, c)
			c.rooms[cmd.room] = struct{}{}
		} else {
			delFrom(h.rooms, cmd.room, c)
			delete(c.rooms, cmd.room)
		}
	}
}

// remove is idempotent and only ever called from the Run goroutine.
func (h *Hub) remove(c *Client) {
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	delFrom(h.byUser, c.userID, c)
	for room := range c.rooms {
		delFrom(h.rooms, room, c)
	}
	close(c.send)
}

// addTo / delFrom maintain the set-of-clients maps, pruning empty sets.
func addTo(m map[string]map[*Client]struct{}, key string, c *Client) {
	set := m[key]
	if set == nil {
		set = make(map[*Client]struct{})
		m[key] = set
	}
	set[c] = struct{}{}
}

func delFrom(m map[string]map[*Client]struct{}, key string, c *Client) {
	if set, ok := m[key]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(m, key)
		}
	}
}

// Broadcast sends payload to every connected client.
func (h *Hub) Broadcast(payload []byte) {
	h.broadcast <- outbound{payload: payload}
}

// SendToUser sends payload to all of a single user's connections (if any).
func (h *Hub) SendToUser(userID string, payload []byte) {
	h.broadcast <- outbound{payload: payload, userID: userID}
}

// BroadcastToRoom sends payload to every member of a room.
func (h *Hub) BroadcastToRoom(room string, payload []byte) {
	h.broadcast <- outbound{payload: payload, room: room}
}

// JoinUser adds all of a user's current connections to a room (server-side —
// call this after checking the user is allowed in the room).
func (h *Hub) JoinUser(userID, room string) {
	h.roomCmd <- roomCmd{userID: userID, room: room, join: true}
}

// LeaveUser removes all of a user's connections from a room.
func (h *Hub) LeaveUser(userID, room string) {
	h.roomCmd <- roomCmd{userID: userID, room: room, join: false}
}

// Client is a single WebSocket connection. send is written only by the hub;
// rooms is mutated only by the hub goroutine.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	userID string
	role   string
	rooms  map[string]struct{}
}

// readPump pumps inbound messages from the connection into the hub. It speaks a
// small JSON control protocol (join/leave/message); anything that doesn't parse
// is treated as a plain text broadcast for convenience.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.handleInbound(data)
	}
}

func (c *Client) handleInbound(data []byte) {
	in, ok := decode(data)
	if !ok {
		// not JSON — treat the raw bytes as a broadcast message body
		c.hub.Broadcast(encode(Message{Type: "message", From: c.userID, Body: string(data)}))
		return
	}

	switch in.Type {
	case "join":
		// ponytail: open join. Add a permission check here (is c.userID
		// allowed in in.Room?) before honoring it, or drive joins server-side
		// with hub.JoinUser instead of trusting the client.
		c.hub.roomCmd <- roomCmd{client: c, room: in.Room, join: true}
	case "leave":
		c.hub.roomCmd <- roomCmd{client: c, room: in.Room, join: false}
	default: // "message"
		out := encode(Message{Type: "message", From: c.userID, Room: in.Room, Body: in.Body})
		if in.Room != "" {
			c.hub.BroadcastToRoom(in.Room, out)
		} else {
			c.hub.Broadcast(out)
		}
	}
}

// writePump pumps hub messages to the connection and sends periodic pings.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok { // hub closed the channel
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
