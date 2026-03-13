package activity

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	redis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	presenceRedisTimeout    = 2 * time.Second
	presenceExpireAfter     = 10 * time.Minute
	redisPresenceRoomsKey   = "mx:activity:presence:rooms"
	redisPresenceSIDRoomKey = "mx:activity:presence:sid_room"
	redisPresenceRoomPrefix = "mx:activity:presence:room:"
)

func upsertPresence(dto presenceUpdateDTO, ip string) presenceRecord {
	now := nowMillis()
	connectedAt := now

	roomName := strings.TrimSpace(dto.RoomName)
	sid := strings.TrimSpace(dto.SID)
	rdb := presenceRedis()
	if rdb != nil && roomName != "" && sid != "" {
		ctx, cancel := presenceContext()
		defer cancel()

		if prev, ok := presenceRecordOfSID(ctx, rdb, roomName, sid); ok {
			connectedAt = prev.ConnectedAt
		} else {
			removePresenceIfExpired(ctx, rdb, roomName, sid)
		}

		oldRoom, err := rdb.HGet(ctx, redisPresenceSIDRoomKey, sid).Result()
		switch {
		case err == nil:
			oldRoom = strings.TrimSpace(oldRoom)
			if oldRoom != "" && oldRoom != roomName {
				removePresenceFromRoom(ctx, rdb, oldRoom, sid)
			}
		case err != redis.Nil:
			presenceLogger().Warn("lookup presence room failed", zap.String("sid", sid), zap.Error(err))
		}
	}

	record := presenceRecord{
		Identity:      dto.Identity,
		RoomName:      roomName,
		Position:      dto.Position,
		SID:           sid,
		DisplayName:   dto.DisplayName,
		ReaderID:      dto.ReaderID,
		OperationTime: dto.TS,
		UpdatedAt:     now,
		ConnectedAt:   connectedAt,
		JoinedAt:      connectedAt,
		IP:            ip,
	}

	if rdb != nil && roomName != "" && sid != "" {
		ctx, cancel := presenceContext()
		defer cancel()

		payload, err := json.Marshal(record)
		if err != nil {
			presenceLogger().Warn("encode presence record failed", zap.String("sid", sid), zap.String("room", roomName), zap.Error(err))
			return record
		}

		pipe := rdb.TxPipeline()
		pipe.HSet(ctx, presenceRoomKey(roomName), sid, payload)
		pipe.SAdd(ctx, redisPresenceRoomsKey, roomName)
		pipe.HSet(ctx, redisPresenceSIDRoomKey, sid, roomName)
		if _, err := pipe.Exec(ctx); err != nil {
			presenceLogger().Warn("persist presence record failed", zap.String("sid", sid), zap.String("room", roomName), zap.Error(err))
		}
	}

	return record
}

func listRoomPresence(roomName string) []presenceRecord {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return nil
	}

	rdb := presenceRedis()
	if rdb == nil {
		return nil
	}

	ctx, cancel := presenceContext()
	defer cancel()

	entries, staleSIDs := readRoomPresence(ctx, rdb, roomName)
	if len(staleSIDs) > 0 {
		removePresenceMembers(ctx, rdb, roomName, staleSIDs)
	}
	return entries
}

func getAllRooms() ([]string, map[string]int) {
	rdb := presenceRedis()
	if rdb == nil {
		return nil, map[string]int{}
	}

	ctx, cancel := presenceContext()
	defer cancel()

	roomNames, err := rdb.SMembers(ctx, redisPresenceRoomsKey).Result()
	if err != nil {
		presenceLogger().Warn("list presence rooms failed", zap.Error(err))
		return nil, map[string]int{}
	}

	rooms := make([]string, 0, len(roomNames))
	roomCount := map[string]int{}
	staleRooms := make([]string, 0)
	for _, roomName := range roomNames {
		roomName = strings.TrimSpace(roomName)
		if roomName == "" {
			continue
		}
		entries, staleSIDs := readRoomPresence(ctx, rdb, roomName)
		if len(staleSIDs) > 0 {
			removePresenceMembers(ctx, rdb, roomName, staleSIDs)
		}
		if len(entries) == 0 {
			staleRooms = append(staleRooms, roomName)
			continue
		}
		rooms = append(rooms, roomName)
		roomCount[roomName] = len(entries)
	}
	if len(staleRooms) > 0 {
		removePresenceRooms(ctx, rdb, staleRooms)
	}
	sort.Strings(rooms)
	return rooms, roomCount
}

func prunePresenceBySocketState(
	hasSID func(string) bool,
	sidInRoom func(string, string) bool,
) {
	if hasSID == nil || sidInRoom == nil {
		return
	}

	rdb := presenceRedis()
	if rdb == nil {
		return
	}

	ctx, cancel := presenceContext()
	defer cancel()

	roomNames, err := rdb.SMembers(ctx, redisPresenceRoomsKey).Result()
	if err != nil {
		presenceLogger().Warn("list presence rooms for prune failed", zap.Error(err))
		return
	}

	staleRooms := make([]string, 0)
	for _, roomName := range roomNames {
		roomName = strings.TrimSpace(roomName)
		if roomName == "" {
			continue
		}
		entries, staleSIDs := readRoomPresence(ctx, rdb, roomName)
		for _, entry := range entries {
			if !hasSID(entry.SID) || !sidInRoom(entry.SID, roomName) {
				staleSIDs = append(staleSIDs, entry.SID)
			}
		}
		if len(staleSIDs) > 0 {
			removePresenceMembers(ctx, rdb, roomName, staleSIDs)
		}
		remaining, _ := readRoomPresence(ctx, rdb, roomName)
		if len(remaining) == 0 {
			staleRooms = append(staleRooms, roomName)
		}
	}
	if len(staleRooms) > 0 {
		removePresenceRooms(ctx, rdb, staleRooms)
	}
}

func readRoomPresence(ctx context.Context, rdb *redis.Client, roomName string) ([]presenceRecord, []string) {
	values, err := rdb.HGetAll(ctx, presenceRoomKey(roomName)).Result()
	if err != nil {
		presenceLogger().Warn("read presence room failed", zap.String("room", roomName), zap.Error(err))
		return nil, nil
	}

	expireBefore := nowMillis() - int64(presenceExpireAfter/time.Millisecond)
	entries := make([]presenceRecord, 0, len(values))
	staleSIDs := make([]string, 0)
	for sid, raw := range values {
		entry, ok := unmarshalPresenceRecord(raw)
		if !ok || entry.UpdatedAt < expireBefore {
			staleSIDs = append(staleSIDs, sid)
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].UpdatedAt > entries[j].UpdatedAt })
	return entries, staleSIDs
}

func presenceRecordOfSID(ctx context.Context, rdb *redis.Client, roomName, sid string) (presenceRecord, bool) {
	raw, err := rdb.HGet(ctx, presenceRoomKey(roomName), sid).Result()
	if err != nil {
		return presenceRecord{}, false
	}
	entry, ok := unmarshalPresenceRecord(raw)
	if !ok {
		return presenceRecord{}, false
	}
	if entry.UpdatedAt < nowMillis()-int64(presenceExpireAfter/time.Millisecond) {
		return presenceRecord{}, false
	}
	return entry, true
}

func removePresenceIfExpired(ctx context.Context, rdb *redis.Client, roomName, sid string) {
	entry, ok := presenceRecordOfSID(ctx, rdb, roomName, sid)
	if ok {
		if entry.UpdatedAt >= nowMillis()-int64(presenceExpireAfter/time.Millisecond) {
			return
		}
	}
	removePresenceFromRoom(ctx, rdb, roomName, sid)
}

func removePresenceFromRoom(ctx context.Context, rdb *redis.Client, roomName, sid string) {
	removePresenceMembers(ctx, rdb, roomName, []string{sid})
}

func removePresenceMembers(ctx context.Context, rdb *redis.Client, roomName string, sids []string) {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" || len(sids) == 0 {
		return
	}

	memberArgs := make([]string, 0, len(sids))
	for _, sid := range sids {
		sid = strings.TrimSpace(sid)
		if sid == "" {
			continue
		}
		memberArgs = append(memberArgs, sid)
	}
	if len(memberArgs) == 0 {
		return
	}

	if err := rdb.HDel(ctx, presenceRoomKey(roomName), memberArgs...).Err(); err != nil {
		presenceLogger().Warn("remove presence members failed", zap.String("room", roomName), zap.Error(err))
		return
	}
	for _, sid := range memberArgs {
		mappedRoom, err := rdb.HGet(ctx, redisPresenceSIDRoomKey, sid).Result()
		if err == nil && strings.TrimSpace(mappedRoom) == roomName {
			_ = rdb.HDel(ctx, redisPresenceSIDRoomKey, sid).Err()
		}
	}
	removePresenceRoomsIfEmpty(ctx, rdb, roomName)
}

func removePresenceRooms(ctx context.Context, rdb *redis.Client, roomNames []string) {
	if len(roomNames) == 0 {
		return
	}
	members := make([]interface{}, 0, len(roomNames))
	for _, roomName := range roomNames {
		roomName = strings.TrimSpace(roomName)
		if roomName == "" {
			continue
		}
		members = append(members, roomName)
		_ = rdb.Del(ctx, presenceRoomKey(roomName)).Err()
	}
	if len(members) == 0 {
		return
	}
	_ = rdb.SRem(ctx, redisPresenceRoomsKey, members...).Err()
}

func removePresenceRoomsIfEmpty(ctx context.Context, rdb *redis.Client, roomName string) {
	count, err := rdb.HLen(ctx, presenceRoomKey(roomName)).Result()
	if err != nil {
		presenceLogger().Warn("presence room count failed", zap.String("room", roomName), zap.Error(err))
		return
	}
	if count > 0 {
		return
	}
	pipe := rdb.TxPipeline()
	pipe.Del(ctx, presenceRoomKey(roomName))
	pipe.SRem(ctx, redisPresenceRoomsKey, roomName)
	_, _ = pipe.Exec(ctx)
}

func unmarshalPresenceRecord(raw string) (presenceRecord, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return presenceRecord{}, false
	}
	var entry presenceRecord
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		presenceLogger().Warn("decode presence record failed", zap.Error(err))
		return presenceRecord{}, false
	}
	if strings.TrimSpace(entry.RoomName) == "" || strings.TrimSpace(entry.SID) == "" {
		return presenceRecord{}, false
	}
	return entry, true
}

func presenceRedis() *redis.Client {
	if pkgredis.Default == nil {
		return nil
	}
	return pkgredis.Default.Raw()
}

func presenceContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), presenceRedisTimeout)
}

func presenceRoomKey(roomName string) string {
	return redisPresenceRoomPrefix + strings.TrimSpace(roomName)
}

func presenceLogger() *zap.Logger {
	return zap.L().Named("ActivityPresence")
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
