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
		sidSession:          make(map[string]string),
		sidIdentity:         make(map[string]string),
		roomCount:           make(map[string]int),
		publicSessionCount:  make(map[string]int),
		sidJoinedRooms:      make(map[string]map[string]struct{}),
		joinedRoomCount:     make(map[string]int),
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
	sessionID := normalizeSessionID(c.sessionID, c.sid)

	h.mu.Lock()
	if presetSessionID := normalizeSessionID(h.sidSession[c.sid], c.sid); presetSessionID != "" {
		sessionID = presetSessionID
	}
	if oldRoom, ok := h.sidRoom[c.sid]; ok {
		oldSessionID := normalizeSessionID(h.sidSession[c.sid], c.sid)
		if oldRoom == c.room && oldSessionID == sessionID {
			h.mu.Unlock()
			return
		}
		if h.roomCount[oldRoom] > 0 {
			h.roomCount[oldRoom]--
		}
		if oldRoom == RoomPublic {
			h.decreasePublicSessionCountLocked(oldSessionID)
			h.clearJoinedRoomsLocked(c.sid)
		}
	}

	h.sidRoom[c.sid] = c.room
	h.sidSession[c.sid] = sessionID
	h.roomCount[c.room]++
	if c.room == RoomPublic {
		h.increasePublicSessionCountLocked(sessionID)
		if _, ok := h.sidJoinedRooms[c.sid]; !ok {
			h.sidJoinedRooms[c.sid] = map[string]struct{}{}
		}
		shouldBroadcastOnline = true
		currentOnline = len(h.publicSessionCount)
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
		delete(h.sidSession, c.sid)
		h.clearJoinedRoomsLocked(c.sid)
		h.mu.Unlock()
		return
	}
	sessionID := normalizeSessionID(h.sidSession[c.sid], c.sid)

	delete(h.sidRoom, c.sid)
	delete(h.sidSession, c.sid)
	delete(h.sidIdentity, c.sid)
	if h.roomCount[room] > 0 {
		h.roomCount[room]--
	}
	h.clearJoinedRoomsLocked(c.sid)
	if room == RoomPublic {
		h.decreasePublicSessionCountLocked(sessionID)
		shouldBroadcastOffline = true
		currentOnline = len(h.publicSessionCount)
	}
	h.mu.Unlock()

	if shouldBroadcastOffline {
		h.BroadcastPublic(eventVisitorOffline, newVisitorEventPayload(currentOnline, sessionID))
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

func normalizeSessionID(sessionID, fallback string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		return sessionID
	}
	return strings.TrimSpace(fallback)
}

func (h *Hub) increasePublicSessionCountLocked(sessionID string) {
	if sessionID == "" {
		return
	}
	h.publicSessionCount[sessionID]++
}

func (h *Hub) decreasePublicSessionCountLocked(sessionID string) {
	if sessionID == "" {
		return
	}
	if h.publicSessionCount[sessionID] <= 1 {
		delete(h.publicSessionCount, sessionID)
		return
	}
	h.publicSessionCount[sessionID]--
}

func (h *Hub) clearJoinedRoomsLocked(sid string) {
	joined := h.sidJoinedRooms[sid]
	if len(joined) == 0 {
		delete(h.sidJoinedRooms, sid)
		return
	}
	for roomName := range joined {
		if h.joinedRoomCount[roomName] <= 1 {
			delete(h.joinedRoomCount, roomName)
		} else {
			h.joinedRoomCount[roomName]--
		}
	}
	delete(h.sidJoinedRooms, sid)
}

func (h *Hub) joinPublicRoom(sid, roomName string) bool {
	roomName = strings.TrimSpace(roomName)
	if sid == "" || roomName == "" {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.sidJoinedRooms[sid]; !ok {
		h.sidJoinedRooms[sid] = map[string]struct{}{}
	}
	if _, exists := h.sidJoinedRooms[sid][roomName]; exists {
		return false
	}
	h.sidJoinedRooms[sid][roomName] = struct{}{}
	h.joinedRoomCount[roomName]++
	return true
}

func (h *Hub) leavePublicRoom(sid, roomName string) bool {
	roomName = strings.TrimSpace(roomName)
	if sid == "" || roomName == "" {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	joined := h.sidJoinedRooms[sid]
	if len(joined) == 0 {
		return false
	}
	if _, exists := joined[roomName]; !exists {
		return false
	}

	delete(joined, roomName)
	if len(joined) == 0 {
		delete(h.sidJoinedRooms, sid)
	}
	if h.joinedRoomCount[roomName] <= 1 {
		delete(h.joinedRoomCount, roomName)
	} else {
		h.joinedRoomCount[roomName]--
	}
	return true
}

func (h *Hub) updateClientSession(sid, sessionID string) (string, bool, int) {
	sid = strings.TrimSpace(sid)
	sessionID = normalizeSessionID(sessionID, sid)
	if sid == "" || sessionID == "" {
		return "", false, h.ClientCount(RoomPublic)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.sidRoom[sid]
	if !ok {
		h.sidSession[sid] = sessionID
		return sessionID, false, len(h.publicSessionCount)
	}
	oldSessionID := normalizeSessionID(h.sidSession[sid], sid)
	if oldSessionID == sessionID {
		return oldSessionID, false, len(h.publicSessionCount)
	}

	h.sidSession[sid] = sessionID
	if room == RoomPublic {
		h.decreasePublicSessionCountLocked(oldSessionID)
		h.increasePublicSessionCountLocked(sessionID)
	}
	return sessionID, true, len(h.publicSessionCount)
}

func (h *Hub) sessionIDOfSID(sid string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return normalizeSessionID(h.sidSession[sid], sid)
}

func (h *Hub) SetSIDIdentity(sid, identity string) {
	sid = strings.TrimSpace(sid)
	identity = strings.TrimSpace(identity)
	if sid == "" || identity == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sidIdentity[sid] = identity
}

func (h *Hub) identityOfSID(sid, fallback string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	identity := strings.TrimSpace(h.sidIdentity[sid])
	if identity != "" {
		return identity
	}
	return normalizeSessionID(fallback, sid)
}

func (h *Hub) joinedPublicRoomsOfSID(sid string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	joined := h.sidJoinedRooms[sid]
	if len(joined) == 0 {
		return nil
	}
	rooms := make([]string, 0, len(joined))
	for roomName := range joined {
		rooms = append(rooms, roomName)
	}
	return rooms
}

func (h *Hub) PublicRoomCount() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make(map[string]int, len(h.joinedRoomCount))
	for roomName, count := range h.joinedRoomCount {
		if count > 0 {
			out[roomName] = count
		}
	}
	return out
}

func (h *Hub) HasSID(sid string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.sidRoom[sid]
	return ok
}

func (h *Hub) SIDInPublicRoom(sid, roomName string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	joined := h.sidJoinedRooms[sid]
	if len(joined) == 0 {
		return false
	}
	_, ok := joined[roomName]
	return ok
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
	if room == RoomPublic {
		return len(h.publicSessionCount)
	}
	return h.roomCount[room]
}

// Handler returns the socket.io HTTP handler mounted at /socket.io.
func (h *Hub) Handler() http.Handler {
	return h.sio.ServeHandler(nil)
}
