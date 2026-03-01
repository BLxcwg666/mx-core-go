package activity

import (
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

func upsertPresence(dto presenceUpdateDTO, ip string) presenceRecord {
	now := nowMillis()
	presenceStore.mu.Lock()
	defer presenceStore.mu.Unlock()

	cleanupPresenceLocked(now)

	if _, ok := presenceStore.rooms[dto.RoomName]; !ok {
		presenceStore.rooms[dto.RoomName] = map[string]presenceRecord{}
	}

	prev, hasPrev := presenceStore.rooms[dto.RoomName][dto.SID]
	connectedAt := now
	if hasPrev {
		connectedAt = prev.ConnectedAt
	}

	record := presenceRecord{
		Identity:      dto.Identity,
		RoomName:      dto.RoomName,
		Position:      dto.Position,
		SID:           dto.SID,
		DisplayName:   dto.DisplayName,
		ReaderID:      dto.ReaderID,
		OperationTime: dto.TS,
		UpdatedAt:     now,
		ConnectedAt:   connectedAt,
		JoinedAt:      connectedAt,
		IP:            ip,
	}
	presenceStore.rooms[dto.RoomName][dto.SID] = record
	return record
}

func listRoomPresence(roomName string) []presenceRecord {
	now := nowMillis()
	presenceStore.mu.Lock()
	defer presenceStore.mu.Unlock()

	cleanupPresenceLocked(now)

	room := presenceStore.rooms[roomName]
	out := make([]presenceRecord, 0, len(room))
	for _, item := range room {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func getAllRooms() ([]string, map[string]int) {
	now := nowMillis()
	presenceStore.mu.Lock()
	defer presenceStore.mu.Unlock()

	cleanupPresenceLocked(now)

	rooms := make([]string, 0, len(presenceStore.rooms))
	roomCount := map[string]int{}
	for roomName, room := range presenceStore.rooms {
		if len(room) == 0 {
			continue
		}
		rooms = append(rooms, roomName)
		roomCount[roomName] = len(room)
	}
	sort.Strings(rooms)
	return rooms, roomCount
}

func cleanupPresenceLocked(now int64) {
	expireBefore := now - int64((10 * time.Minute).Milliseconds())
	for roomName, room := range presenceStore.rooms {
		for sid, item := range room {
			if item.UpdatedAt < expireBefore {
				delete(room, sid)
			}
		}
		if len(room) == 0 {
			delete(presenceStore.rooms, roomName)
		}
	}
}

func sanitizePresence(entry presenceRecord) gin.H {
	return gin.H{
		"identity":      entry.Identity,
		"roomName":      entry.RoomName,
		"position":      entry.Position,
		"sid":           entry.SID,
		"displayName":   entry.DisplayName,
		"readerId":      entry.ReaderID,
		"operationTime": entry.OperationTime,
		"updatedAt":     entry.UpdatedAt,
		"connectedAt":   entry.ConnectedAt,
		"joinedAt":      entry.JoinedAt,
	}
}
