package gateway

import (
	"context"
	"encoding/json"
)

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
