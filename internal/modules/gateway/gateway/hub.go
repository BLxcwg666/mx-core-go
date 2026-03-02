package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	redis "github.com/redis/go-redis/v9"
	socketio "github.com/zishang520/socket.io/v2/socket"
	"go.uber.org/zap"
)

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

// Run starts the hub loop and Redis subscriber.
func (h *Hub) Run(ctx context.Context) {
	go h.subscribeRedis(ctx)

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

func (h *Hub) registerClient(c clientMeta) {
	shouldBroadcastOnline := false
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
		shouldBroadcastOnline = true
		currentOnline = h.roomCount[RoomPublic]
	}
	h.mu.Unlock()

	if shouldBroadcastOnline {
		h.BroadcastPublic(eventVisitorOnline, newVisitorEventPayload(currentOnline, ""))
		h.updateDailyOnlineStats(currentOnline)
	}
}

func (h *Hub) unregisterClient(c clientMeta) {
	shouldBroadcastOffline := false
	currentOnline := 0

	h.mu.Lock()
	room, ok := h.sidRoom[c.sid]
	if !ok {
		h.mu.Unlock()
		return
	}

	delete(h.sidRoom, c.sid)
	if h.roomCount[room] > 0 {
		h.roomCount[room]--
	}
	if room == RoomPublic {
		shouldBroadcastOffline = true
		currentOnline = h.roomCount[RoomPublic]
	}
	h.mu.Unlock()

	if shouldBroadcastOffline {
		h.BroadcastPublic(eventVisitorOffline, newVisitorEventPayload(currentOnline, c.sessionID))
	}
}

func (h *Hub) updateDailyOnlineStats(currentOnline int) {
	if currentOnline < 0 {
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

func newVisitorEventPayload(online int, sessionID string) map[string]interface{} {
	payload := map[string]interface{}{
		"online":    online,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
	}
	return payload
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
