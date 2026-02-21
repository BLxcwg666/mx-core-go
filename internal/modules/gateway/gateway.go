package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"go.uber.org/zap"
)

const (
	RoomAdmin       = "admin"
	RoomPublic      = "public"
	redisChanAdmin  = "mx:gateway:admin"
	redisChanPublic = "mx:gateway:public"
)

// Message is the envelope for all WebSocket events.
type Message struct {
	Event   string      `json:"event"`
	Payload interface{} `json:"payload"`
	Room    string      `json:"-"` // routing only, not serialized
}

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	room string
	once sync.Once
}

func (c *Client) close() {
	c.once.Do(func() {
		c.hub.unregister <- c
		c.conn.Close()
	})
}

// writePump drains the send channel to the WebSocket.
func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads from the WebSocket (keeps connection alive, handles pong).
func (c *Client) readPump() {
	defer c.close()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// Hub manages all WebSocket clients and routes broadcasts.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*Client]bool
	broadcast  chan Message
	register   chan *Client
	unregister chan *Client
	rc         *pkgredis.Client
	logger     *zap.Logger
}

func NewHub(rc *pkgredis.Client, logger *zap.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan Message, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		rc:         rc,
		logger:     logger,
	}
}

// Run starts the hub's event loop and Redis subscriber.
func (h *Hub) Run(ctx context.Context) {
	if h.rc != nil {
		go h.subscribeRedis(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.deliver(msg)
			if h.rc != nil {
				channel := redisChanPublic
				if msg.Room == RoomAdmin {
					channel = redisChanAdmin
				}
				data, _ := json.Marshal(msg)
				h.rc.Publish(ctx, channel, string(data))
			}
		}
	}
}

func (h *Hub) deliver(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		if msg.Room != "" && client.room != msg.Room {
			continue
		}
		select {
		case client.send <- data:
		default:
			go client.close()
		}
	}
}

// subscribeRedis listens for broadcasts from other server instances.
func (h *Hub) subscribeRedis(ctx context.Context) {
	pubsub := h.rc.Subscribe(ctx, redisChanAdmin, redisChanPublic)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case redisMsg, ok := <-ch:
			if !ok {
				return
			}
			var msg Message
			if err := json.Unmarshal([]byte(redisMsg.Payload), &msg); err != nil {
				continue
			}
			h.deliver(msg)
		}
	}
}

// Broadcast sends an event to all clients in the given room (or all if room="").
func (h *Hub) Broadcast(event string, payload interface{}, room string) {
	h.broadcast <- Message{Event: event, Payload: payload, Room: room}
}

// BroadcastAdmin sends to admin room only.
func (h *Hub) BroadcastAdmin(event string, payload interface{}) {
	h.Broadcast(event, payload, RoomAdmin)
}

// BroadcastPublic sends to the public room.
func (h *Hub) BroadcastPublic(event string, payload interface{}) {
	h.Broadcast(event, payload, RoomPublic)
}

// ClientCount returns the number of connected clients (optionally filtered by room).
func (h *Hub) ClientCount(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if room == "" {
		return len(h.clients)
	}
	count := 0
	for c := range h.clients {
		if c.room == room {
			count++
		}
	}
	return count
}

// upgrader accepts WebSocket upgrade requests.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // CORS handled globally
}

// RegisterRoutes mounts the WebSocket endpoints.
func RegisterRoutes(rg *gin.RouterGroup, hub *Hub, adminAuthFn func(*gin.Context) bool) {
	rg.GET("/gateway", func(c *gin.Context) {
		serveWS(c, hub, RoomPublic)
	})

	rg.GET("/gateway/admin", func(c *gin.Context) {
		if !adminAuthFn(c) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		serveWS(c, hub, RoomAdmin)
	})

	rg.GET("/gateway/stats", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"public": hub.ClientCount(RoomPublic),
			"admin":  hub.ClientCount(RoomAdmin),
			"total":  hub.ClientCount(""),
		})
	})
}

func serveWS(c *gin.Context, hub *Hub, room string) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client := &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 64),
		room: room,
	}
	hub.register <- client

	welcome, _ := json.Marshal(Message{Event: "connected", Payload: gin.H{"room": room}})
	client.send <- welcome

	go client.writePump()
	go client.readPump()
}
