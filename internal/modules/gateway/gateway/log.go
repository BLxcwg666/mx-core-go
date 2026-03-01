package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mx-space/core/internal/pkg/nativelog"
	socketio "github.com/zishang520/socket.io/v2/socket"
	"go.uber.org/zap"
)

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
	paths, err := nativelog.SnapshotFilesSinceStartup(time.Now())
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("gateway log snapshot resolve failed", zap.Error(err))
		}
		return
	}
	if len(paths) == 0 {
		return
	}

	buf := make([]byte, nativeLogSnapshotChunkSize)
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			if h.logger != nil {
				h.logger.Warn("gateway log snapshot open failed", zap.String("path", path), zap.Error(err))
			}
			continue
		}
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
		_ = file.Close()
	}
}
