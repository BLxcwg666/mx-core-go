package gateway

import (
	"sync"

	pkgredis "github.com/mx-space/core/internal/pkg/redis"
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
