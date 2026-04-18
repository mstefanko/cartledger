package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

// Client represents a single WebSocket connection from a user.
type Client struct {
	hub         *Hub
	conn        *websocket.Conn
	send        chan []byte
	HouseholdID string
	UserID      string
}

// Hub maintains the set of active clients per household and broadcasts messages.
type Hub struct {
	// households maps household_id to a set of connected clients.
	households map[string]map[*Client]bool

	// broadcast channel for messages to send to a household.
	broadcast chan Message

	// register requests from clients.
	register chan *Client

	// unregister requests from clients.
	unregister chan *Client

	// mu protects households for read access from Broadcast method.
	mu sync.RWMutex
}

// NewHub creates a new Hub instance.
func NewHub() *Hub {
	return &Hub{
		households: make(map[string]map[*Client]bool),
		broadcast:  make(chan Message, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run starts the hub's main event loop. It should be launched as a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if h.households[client.HouseholdID] == nil {
				h.households[client.HouseholdID] = make(map[*Client]bool)
			}
			h.households[client.HouseholdID][client] = true
			h.mu.Unlock()
			slog.Debug("ws: client registered", "user", client.UserID, "household", client.HouseholdID)

		case client := <-h.unregister:
			h.mu.Lock()
			if clients, ok := h.households[client.HouseholdID]; ok {
				if _, exists := clients[client]; exists {
					delete(clients, client)
					close(client.send)
					if len(clients) == 0 {
						delete(h.households, client.HouseholdID)
					}
				}
			}
			h.mu.Unlock()
			slog.Debug("ws: client unregistered", "user", client.UserID, "household", client.HouseholdID)

		case msg := <-h.broadcast:
			data, err := json.Marshal(msg)
			if err != nil {
				slog.Error("ws: marshal broadcast error", "err", err)
				continue
			}
			h.mu.RLock()
			clients := h.households[msg.Household]
			for client := range clients {
				select {
				case client.send <- data:
				default:
					// Client send buffer full; schedule removal.
					go func(c *Client) {
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends a message to all clients in the specified household.
// Safe to call from any goroutine.
func (h *Hub) Broadcast(msg Message) {
	h.broadcast <- msg
}

// readPump pumps messages from the WebSocket connection to the hub.
// For now, the server ignores incoming messages from clients (read-only push model).
// The pump is still needed to handle control frames (ping/pong/close).
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Warn("ws: read error", "user", c.UserID, "err", err)
			}
			break
		}
		// Incoming messages from clients are ignored in the current design.
	}
}

// writePump pumps messages from the hub to the WebSocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Drain queued messages into the current write.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte("\n"))
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ServeWS registers a new client and starts its read/write pumps.
// Called by the HTTP handler after upgrading the connection.
func ServeWS(hub *Hub, conn *websocket.Conn, householdID, userID string) {
	client := &Client{
		hub:         hub,
		conn:        conn,
		send:        make(chan []byte, 256),
		HouseholdID: householdID,
		UserID:      userID,
	}
	hub.register <- client

	go client.writePump()
	go client.readPump()
}
