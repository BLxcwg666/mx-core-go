package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/nativelog"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	redis "github.com/redis/go-redis/v9"
	socketio "github.com/zishang520/socket.io/v2/socket"
	"go.uber.org/zap"
)

const (
	RoomAdmin       = "admin"
	RoomPublic      = "public"
	namespaceAdmin  = "/admin"
	namespaceWeb    = "/web"
	redisChanAdmin  = "mx:gateway:admin"
	redisChanPublic = "mx:gateway:public"

	redisKeyMaxOnlineCount      = "mx:max_online_count"
	redisKeyMaxOnlineCountTotal = "mx:max_online_count:total"

	nativeLogSnapshotChunkSize = 32 * 1024
)

// Message is the envelope used by hub broadcasts and Redis fan-out.
type Message struct {
	Event   string      `json:"event"`
	Payload interface{} `json:"payload"`
	Code    *int        `json:"code,omitempty"`
	Room    string      `json:"room,omitempty"`
}

type gatewayPayload struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
	Code *int        `json:"code,omitempty"`
}

type clientMeta struct {
	sid  string
	room string
}

type adminLogSubscription struct {
	streamID int
	stopCh   chan struct{}
}

// Hub manages socket.io namespaces and cluster fan-out.
type Hub struct {
	mu sync.RWMutex

	sidRoom   map[string]string
	roomCount map[string]int

	logSubMu sync.Mutex
	logSubs  map[string]adminLogSubscription

	broadcast  chan Message
	register   chan clientMeta
	unregister chan clientMeta

	rc                  *pkgredis.Client
	logger              *zap.Logger
	sio                 *socketio.Server
	adminTokenValidator func(string) bool
}

func NewHub(rc *pkgredis.Client, logger *zap.Logger, adminTokenValidator func(string) bool) *Hub {
	sio := socketio.NewServer(nil, nil)
	h := &Hub{
		sidRoom:             make(map[string]string),
		roomCount:           make(map[string]int),
		logSubs:             make(map[string]adminLogSubscription),
		broadcast:           make(chan Message, 256),
		register:            make(chan clientMeta, 256),
		unregister:          make(chan clientMeta, 256),
		rc:                  rc,
		logger:              logger,
		sio:                 sio,
		adminTokenValidator: adminTokenValidator,
	}
	h.registerNamespaces()
	return h
}

func (h *Hub) registerNamespaces() {
	webNS := h.sio.Of(namespaceWeb, nil)
	_ = webNS.On("connection", func(args ...any) {
		client, ok := args[0].(*socketio.Socket)
		if !ok {
			return
		}
		sid := string(client.Id())
		h.register <- clientMeta{sid: sid, room: RoomPublic}
		_ = client.Emit("message", h.gatewayMessageFormat("GATEWAY_CONNECT", "WebSocket connected", nil))

		_ = client.On("disconnect", func(_ ...any) {
			h.unregister <- clientMeta{sid: sid, room: RoomPublic}
		})
	})

	adminNS := h.sio.Of(namespaceAdmin, nil)
	_ = adminNS.On("connection", func(args ...any) {
		client, ok := args[0].(*socketio.Socket)
		if !ok {
			return
		}

		token := normalizeToken(extractToken(client))
		if token == "" || h.adminTokenValidator == nil || !h.adminTokenValidator(token) {
			_ = client.Emit("message", h.gatewayMessageFormat("AUTH_FAILED", "auth failed", nil))
			client.Disconnect(true)
			return
		}

		sid := string(client.Id())
		h.register <- clientMeta{sid: sid, room: RoomAdmin}
		_ = client.Emit("message", h.gatewayMessageFormat("GATEWAY_CONNECT", "WebSocket connected", nil))

		_ = client.On("log", func(eventArgs ...any) {
			h.subscribeStdout(client, parsePrevLogOption(eventArgs))
		})
		_ = client.On("unlog", func(_ ...any) {
			h.unsubscribeStdout(sid)
		})

		_ = client.On("disconnect", func(_ ...any) {
			h.unsubscribeStdout(sid)
			h.unregister <- clientMeta{sid: sid, room: RoomAdmin}
		})
	})
}

func extractToken(client *socketio.Socket) string {
	handshake := client.Handshake()
	if handshake == nil {
		return ""
	}
	if token := firstValueFromMultiMap(handshake.Query, "token"); token != "" {
		return token
	}
	if token := firstValueFromMultiMap(handshake.Headers, "authorization"); token != "" {
		return token
	}
	return ""
}

func firstValueFromMultiMap(values map[string][]string, key string) string {
	if len(values) == 0 {
		return ""
	}
	for k, list := range values {
		if !strings.EqualFold(strings.TrimSpace(k), key) || len(list) == 0 {
			continue
		}
		v := strings.TrimSpace(list[0])
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeToken(raw string) string {
	token := strings.TrimSpace(raw)
	if token == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return strings.TrimSpace(token[7:])
	}
	return token
}

func parsePrevLogOption(args []any) bool {
	if len(args) == 0 {
		return true
	}
	return extractPrevLog(args[0], true)
}

func extractPrevLog(raw any, fallback bool) bool {
	switch v := raw.(type) {
	case map[string]any:
		if value, ok := v["prevLog"]; ok {
			return toBool(value, fallback)
		}
	case string:
		payload := make(map[string]any)
		if err := json.Unmarshal([]byte(v), &payload); err == nil {
			if value, ok := payload["prevLog"]; ok {
				return toBool(value, fallback)
			}
		}
	}
	return fallback
}

func toBool(raw any, fallback bool) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	case float32:
		return v != 0
	case float64:
		return v != 0
	}
	return fallback
}

func (h *Hub) subscribeStdout(client *socketio.Socket, prevLog bool) {
	sid := string(client.Id())
	if sid == "" {
		return
	}

	h.logSubMu.Lock()
	if _, exists := h.logSubs[sid]; exists {
		h.logSubMu.Unlock()
		return
	}
	streamID, stream := nativelog.Subscribe(512)
	stopCh := make(chan struct{})
	h.logSubs[sid] = adminLogSubscription{
		streamID: streamID,
		stopCh:   stopCh,
	}
	h.logSubMu.Unlock()

	if prevLog {
		h.emitNativeLogSnapshot(client)
	}

	go func() {
		for {
			select {
			case <-stopCh:
				return
			case frame, ok := <-stream:
				if !ok {
					return
				}
				if frame == "" {
					continue
				}
				_ = client.Emit("message", h.gatewayMessageFormat("STDOUT", frame, nil))
			}
		}
	}()
}

func (h *Hub) unsubscribeStdout(sid string) {
	if sid == "" {
		return
	}

	h.logSubMu.Lock()
	sub, exists := h.logSubs[sid]
	if exists {
		delete(h.logSubs, sid)
	}
	h.logSubMu.Unlock()
	if !exists {
		return
	}

	close(sub.stopCh)
	nativelog.Unsubscribe(sub.streamID)
}

func (h *Hub) emitNativeLogSnapshot(client *socketio.Socket) {
	path := nativelog.TodayFilePath(time.Now())
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	buf := make([]byte, nativeLogSnapshotChunkSize)
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			_ = client.Emit("message", h.gatewayMessageFormat("STDOUT", string(buf[:n]), nil))
		}
		if readErr == nil {
			continue
		}
		if !errors.Is(readErr, io.EOF) && h.logger != nil {
			h.logger.Warn("gateway log snapshot read failed", zap.String("path", path), zap.Error(readErr))
		}
		break
	}
}

// Run starts the hub loop and Redis subscriber.
func (h *Hub) Run(ctx context.Context) {
	if h.rc != nil {
		go h.subscribeRedis(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			h.sio.Close(nil)
			return

		case c := <-h.register:
			h.registerClient(c)

		case c := <-h.unregister:
			h.unregisterClient(c)

		case msg := <-h.broadcast:
			h.deliver(msg)

			if h.rc != nil {
				channel := redisChanPublic
				if msg.Room == RoomAdmin {
					channel = redisChanAdmin
				}
				data, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				if err := h.rc.Publish(ctx, channel, string(data)); err != nil && h.logger != nil {
					h.logger.Warn("gateway publish failed", zap.String("channel", channel), zap.Error(err))
				}
			}
		}
	}
}

func (h *Hub) registerClient(c clientMeta) {
	trackOnline := false
	currentOnline := 0

	h.mu.Lock()
	if oldRoom, ok := h.sidRoom[c.sid]; ok {
		if oldRoom == c.room {
			h.mu.Unlock()
			return
		}
		if h.roomCount[oldRoom] > 0 {
			h.roomCount[oldRoom]--
		}
	}

	h.sidRoom[c.sid] = c.room
	h.roomCount[c.room]++
	if c.room == RoomPublic {
		trackOnline = true
		currentOnline = h.roomCount[RoomPublic]
	}
	h.mu.Unlock()

	if trackOnline {
		h.updateDailyOnlineStats(currentOnline)
	}
}

func (h *Hub) unregisterClient(c clientMeta) {
	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.sidRoom[c.sid]
	if !ok {
		return
	}

	delete(h.sidRoom, c.sid)
	if h.roomCount[room] > 0 {
		h.roomCount[room]--
	}
}

func (h *Hub) updateDailyOnlineStats(currentOnline int) {
	if h.rc == nil || currentOnline < 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dateKey := shortDateKey(time.Now())

	maxOnline := 0
	currentMax, err := h.rc.Raw().HGet(ctx, redisKeyMaxOnlineCount, dateKey).Result()
	switch {
	case err == nil:
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(currentMax)); parseErr == nil {
			maxOnline = parsed
		}
	case err == redis.Nil:
		// no-op
	default:
		if h.logger != nil {
			h.logger.Warn("gateway get max online failed", zap.Error(err))
		}
	}

	if currentOnline > maxOnline {
		if err := h.rc.Raw().HSet(ctx, redisKeyMaxOnlineCount, dateKey, currentOnline).Err(); err != nil && h.logger != nil {
			h.logger.Warn("gateway set max online failed", zap.Error(err))
		}
	}

	if err := h.rc.Raw().HIncrBy(ctx, redisKeyMaxOnlineCountTotal, dateKey, 1).Err(); err != nil && h.logger != nil {
		h.logger.Warn("gateway incr online total failed", zap.Error(err))
	}
}

func shortDateKey(t time.Time) string {
	return t.Format("1-2-06")
}

func (h *Hub) gatewayMessageFormat(event string, payload interface{}, code *int) gatewayPayload {
	return gatewayPayload{
		Type: event,
		Data: payload,
		Code: code,
	}
}

func (h *Hub) emitNamespace(nsp string, msg Message) {
	h.sio.Of(nsp, nil).Emit("message", h.gatewayMessageFormat(msg.Event, msg.Payload, msg.Code))
}

func (h *Hub) deliver(msg Message) {
	switch msg.Room {
	case RoomAdmin:
		h.emitNamespace(namespaceAdmin, msg)
	case RoomPublic:
		h.emitNamespace(namespaceWeb, msg)
	case "":
		h.emitNamespace(namespaceAdmin, msg)
		h.emitNamespace(namespaceWeb, msg)
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
		return len(h.sidRoom)
	}
	return h.roomCount[room]
}

// Handler returns the socket.io HTTP handler mounted at /socket.io.
func (h *Hub) Handler() http.Handler {
	return h.sio.ServeHandler(nil)
}

// RegisterRoutes mounts socket.io and stats endpoints.
func RegisterRoutes(rg *gin.RouterGroup, hub *Hub) {
	handler := gin.WrapH(hub.Handler())
	rg.Any("/socket.io", handler)
	rg.Any("/socket.io/*any", handler)

	rg.GET("/gateway/stats", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"public": hub.ClientCount(RoomPublic),
			"admin":  hub.ClientCount(RoomAdmin),
			"total":  hub.ClientCount(""),
		})
	})
}
