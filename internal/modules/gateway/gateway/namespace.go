package gateway

import (
	"strings"

	socketio "github.com/zishang520/socket.io/v2/socket"
)

func (h *Hub) registerNamespaces() {
	webNS := h.sio.Of(namespaceWeb, nil)
	_ = webNS.On("connection", func(args ...any) {
		client, ok := args[0].(*socketio.Socket)
		if !ok {
			return
		}
		sid := string(client.Id())
		sessionID := extractSessionID(client, sid)
		h.register <- clientMeta{sid: sid, room: RoomPublic, sessionID: sessionID}
		_ = client.Emit("message", h.gatewayMessageFormat("GATEWAY_CONNECT", "WebSocket connected", nil))

		_ = client.On("disconnect", func(_ ...any) {
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
