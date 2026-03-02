package gateway

import (
	"encoding/json"
	"strings"

	socketio "github.com/zishang520/socket.io/v2/socket"
)

type inboundWebMessage struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload"`
}

func (h *Hub) registerNamespaces() {
	webNS := h.sio.Of(namespaceWeb, nil)
	_ = webNS.On("connection", func(args ...any) {
		client, ok := args[0].(*socketio.Socket)
		if !ok {
			return
		}
		sid := string(client.Id())
		sessionID := normalizeSessionID(extractSessionID(client, sid), sid)
		h.register <- clientMeta{sid: sid, room: RoomPublic, sessionID: sessionID}
		_ = client.Emit("message", h.gatewayMessageFormat("GATEWAY_CONNECT", "WebSocket connected", nil))
		_ = client.On("message", func(eventArgs ...any) {
			msg, ok := parseInboundWebMessage(eventArgs...)
			if !ok {
				return
			}

			switch msg.Type {
			case messageJoin:
				roomName := firstNonEmptyString(
					strFromAny(msg.Payload["roomName"]),
					strFromAny(msg.Payload["room_name"]),
				)
				if roomName == "" {
					return
				}
				client.Join(socketio.Room(roomName))
				h.joinPublicRoom(sid, roomName)
			case messageLeave:
				roomName := firstNonEmptyString(
					strFromAny(msg.Payload["roomName"]),
					strFromAny(msg.Payload["room_name"]),
				)
				if roomName == "" {
					return
				}
				client.Leave(socketio.Room(roomName))
				if h.leavePublicRoom(sid, roomName) {
					h.BroadcastPublic(eventActivityLeavePresence, newActivityLeavePresencePayload(h.identityOfSID(sid, sessionID), roomName))
				}
			case messageUpdateSID:
				nextSessionID := firstNonEmptyString(
					strFromAny(msg.Payload["sessionId"]),
					strFromAny(msg.Payload["session_id"]),
				)
				if nextSessionID == "" {
					return
				}
				effectiveSessionID, changed, currentOnline := h.updateClientSession(sid, nextSessionID)
				sessionID = normalizeSessionID(effectiveSessionID, sid)
				if !changed {
					return
				}
				h.BroadcastPublic(eventVisitorOnline, newVisitorEventPayload(currentOnline, ""))
				h.updateDailyOnlineStats(currentOnline)
			case messageUpdateLang:
				// compatible no-op
			}
		})

		_ = client.On("disconnect", func(_ ...any) {
			rooms := h.joinedPublicRoomsOfSID(sid)
			identity := h.identityOfSID(sid, sessionID)
			for _, roomName := range rooms {
				h.BroadcastPublic(eventActivityLeavePresence, newActivityLeavePresencePayload(identity, roomName))
			}
			h.unregister <- clientMeta{sid: sid, room: RoomPublic, sessionID: sessionID}
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

func extractSessionID(client *socketio.Socket, fallback string) string {
	handshake := client.Handshake()
	if handshake == nil {
		return fallback
	}
	if sid := firstValueFromMultiMap(handshake.Query, "socket_session_id"); sid != "" {
		return sid
	}
	if sid := firstValueFromMultiMap(handshake.Headers, "x-socket-session-id"); sid != "" {
		return sid
	}
	return fallback
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

func parseInboundWebMessage(args ...any) (inboundWebMessage, bool) {
	if len(args) == 0 || args[0] == nil {
		return inboundWebMessage{}, false
	}

	var msg inboundWebMessage
	switch raw := args[0].(type) {
	case inboundWebMessage:
		msg = raw
	case map[string]interface{}:
		msg.Type = strFromAny(raw["type"])
		msg.Payload = mapFromAny(raw["payload"])
	case string:
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			return inboundWebMessage{}, false
		}
	case []byte:
		if err := json.Unmarshal(raw, &msg); err != nil {
			return inboundWebMessage{}, false
		}
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return inboundWebMessage{}, false
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			return inboundWebMessage{}, false
		}
	}

	msg.Type = strings.TrimSpace(msg.Type)
	if msg.Type == "" {
		return inboundWebMessage{}, false
	}
	if msg.Payload == nil {
		msg.Payload = map[string]interface{}{}
	}
	return msg, true
}

func mapFromAny(v interface{}) map[string]interface{} {
	switch typed := v.(type) {
	case nil:
		return map[string]interface{}{}
	case map[string]interface{}:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return map[string]interface{}{}
		}
		out := map[string]interface{}{}
		if err := json.Unmarshal(data, &out); err != nil {
			return map[string]interface{}{}
		}
		return out
	}
}

func strFromAny(v interface{}) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func newActivityLeavePresencePayload(identity, roomName string) map[string]interface{} {
	return map[string]interface{}{
		"identity": normalizeSessionID(identity, ""),
		"roomName": roomName,
	}
}
