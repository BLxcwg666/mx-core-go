package gateway

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mx-space/core/internal/pkg/cluster"
	redis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	gatewayClusterStateTimeout       = 2 * time.Second
	redisKeyGatewaySIDRoom           = "mx:gateway:sid:room"
	redisKeyGatewaySIDSession        = "mx:gateway:sid:session"
	redisKeyGatewaySIDIdentity       = "mx:gateway:sid:identity"
	redisKeyGatewaySIDInstance       = "mx:gateway:sid:instance"
	redisKeyGatewayPublicSessionRefs = "mx:gateway:public:session_refs"
	redisKeyGatewayAdminSIDs         = "mx:gateway:admin:sids"
	redisKeyGatewayPublicRooms       = "mx:gateway:public:rooms"
	redisKeyGatewayInstanceSIDsPref  = "mx:gateway:instance:sids:"
	redisKeyGatewaySIDRoomsPref      = "mx:gateway:sid:rooms:"
	redisKeyGatewayRoomMembersPref   = "mx:gateway:public:room_members:"
)

func gatewayInstanceKey() string {
	if cluster.IsWorker() && cluster.WorkerID() > 0 {
		return "worker:" + strconv.Itoa(cluster.WorkerID())
	}
	return "single"
}

func gatewayInstanceSIDsKey(instanceKey string) string {
	instanceKey = strings.TrimSpace(instanceKey)
	if instanceKey == "" {
		instanceKey = gatewayInstanceKey()
	}
	return redisKeyGatewayInstanceSIDsPref + instanceKey
}

func gatewaySIDRoomsKey(sid string) string {
	return redisKeyGatewaySIDRoomsPref + strings.TrimSpace(sid)
}

func gatewayRoomMembersKey(roomName string) string {
	return redisKeyGatewayRoomMembersPref + strings.TrimSpace(roomName)
}

func (h *Hub) clusterStateEnabled() bool {
	return h != nil && h.rc != nil && h.rc.Raw() != nil
}

func (h *Hub) clusterContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), gatewayClusterStateTimeout)
}

func (h *Hub) clusterWarn(message string, fields ...zap.Field) {
	if h != nil && h.logger != nil {
		h.logger.Warn(message, fields...)
	}
}

func (h *Hub) initializeClusterState() {
	if !h.clusterStateEnabled() {
		return
	}
	ctx, cancel := h.clusterContext()
	defer cancel()

	instanceKey := gatewayInstanceKey()
	sids, err := h.rc.Raw().SMembers(ctx, gatewayInstanceSIDsKey(instanceKey)).Result()
	if err != nil {
		h.clusterWarn("gateway cluster state bootstrap failed", zap.String("instance", instanceKey), zap.Error(err))
		return
	}
	for _, sid := range sids {
		if err := h.clusterRemoveSIDWithContext(ctx, sid); err != nil {
			h.clusterWarn("gateway stale sid cleanup failed", zap.String("instance", instanceKey), zap.String("sid", sid), zap.Error(err))
		}
	}
	if err := h.rc.Raw().Del(ctx, gatewayInstanceSIDsKey(instanceKey)).Err(); err != nil {
		h.clusterWarn("gateway instance state reset failed", zap.String("instance", instanceKey), zap.Error(err))
	}
}

func (h *Hub) cleanupClusterState() {
	if !h.clusterStateEnabled() {
		return
	}
	ctx, cancel := h.clusterContext()
	defer cancel()

	instanceKey := gatewayInstanceKey()
	sids, err := h.rc.Raw().SMembers(ctx, gatewayInstanceSIDsKey(instanceKey)).Result()
	if err != nil {
		h.clusterWarn("gateway cluster state shutdown failed", zap.String("instance", instanceKey), zap.Error(err))
		return
	}
	for _, sid := range sids {
		if err := h.clusterRemoveSIDWithContext(ctx, sid); err != nil {
			h.clusterWarn("gateway sid shutdown cleanup failed", zap.String("instance", instanceKey), zap.String("sid", sid), zap.Error(err))
		}
	}
	if err := h.rc.Raw().Del(ctx, gatewayInstanceSIDsKey(instanceKey)).Err(); err != nil {
		h.clusterWarn("gateway instance state delete failed", zap.String("instance", instanceKey), zap.Error(err))
	}
}

func (h *Hub) clusterUpsertClient(sid, room, sessionID string) (int, bool) {
	if !h.clusterStateEnabled() {
		return 0, false
	}
	sid = strings.TrimSpace(sid)
	room = strings.TrimSpace(room)
	sessionID = normalizeSessionID(sessionID, sid)
	if sid == "" || room == "" {
		return 0, false
	}

	ctx, cancel := h.clusterContext()
	defer cancel()

	if err := h.clusterRemoveSIDWithContext(ctx, sid); err != nil {
		h.clusterWarn("gateway sid reset failed", zap.String("sid", sid), zap.Error(err))
		return 0, false
	}

	instanceKey := gatewayInstanceKey()
	pipe := h.rc.Raw().TxPipeline()
	pipe.HSet(ctx, redisKeyGatewaySIDRoom, sid, room)
	pipe.HSet(ctx, redisKeyGatewaySIDInstance, sid, instanceKey)
	pipe.SAdd(ctx, gatewayInstanceSIDsKey(instanceKey), sid)
	pipe.HDel(ctx, redisKeyGatewaySIDIdentity, sid)
	pipe.HDel(ctx, redisKeyGatewaySIDSession, sid)
	pipe.SRem(ctx, redisKeyGatewayAdminSIDs, sid)
	if room == RoomPublic {
		pipe.HSet(ctx, redisKeyGatewaySIDSession, sid, sessionID)
		pipe.HIncrBy(ctx, redisKeyGatewayPublicSessionRefs, sessionID, 1)
	}
	if room == RoomAdmin {
		pipe.SAdd(ctx, redisKeyGatewayAdminSIDs, sid)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		h.clusterWarn("gateway sid register failed", zap.String("sid", sid), zap.String("room", room), zap.Error(err))
		return 0, false
	}

	if room != RoomPublic {
		return 0, true
	}
	count, err := h.rc.Raw().HLen(ctx, redisKeyGatewayPublicSessionRefs).Result()
	if err != nil {
		h.clusterWarn("gateway public count refresh failed", zap.String("sid", sid), zap.Error(err))
		return 0, false
	}
	return int(count), true
}

func (h *Hub) clusterRemoveSID(sid string) (int, bool) {
	if !h.clusterStateEnabled() {
		return 0, false
	}
	ctx, cancel := h.clusterContext()
	defer cancel()

	room, err := h.clusterRoomOfSID(ctx, sid)
	if err != nil {
		h.clusterWarn("gateway sid lookup failed", zap.String("sid", sid), zap.Error(err))
		return 0, false
	}
	if err := h.clusterRemoveSIDWithContext(ctx, sid); err != nil {
		h.clusterWarn("gateway sid unregister failed", zap.String("sid", sid), zap.Error(err))
		return 0, false
	}
	if room != RoomPublic {
		return 0, true
	}
	count, err := h.rc.Raw().HLen(ctx, redisKeyGatewayPublicSessionRefs).Result()
	if err != nil {
		h.clusterWarn("gateway public count refresh failed", zap.String("sid", sid), zap.Error(err))
		return 0, false
	}
	return int(count), true
}

func (h *Hub) clusterRemoveSIDWithContext(ctx context.Context, sid string) error {
	if !h.clusterStateEnabled() {
		return nil
	}
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return nil
	}

	rdb := h.rc.Raw()
	room, err := h.clusterRoomOfSID(ctx, sid)
	if err != nil {
		return err
	}
	sessionID, err := h.clusterSessionOfSID(ctx, sid)
	if err != nil {
		return err
	}
	instanceKey, err := h.clusterInstanceOfSID(ctx, sid)
	if err != nil {
		return err
	}
	joinedRooms, err := h.clusterJoinedRoomsWithContext(ctx, sid)
	if err != nil {
		return err
	}

	pipe := rdb.TxPipeline()
	pipe.HDel(ctx, redisKeyGatewaySIDRoom, sid)
	pipe.HDel(ctx, redisKeyGatewaySIDSession, sid)
	pipe.HDel(ctx, redisKeyGatewaySIDIdentity, sid)
	pipe.HDel(ctx, redisKeyGatewaySIDInstance, sid)
	pipe.Del(ctx, gatewaySIDRoomsKey(sid))
	if instanceKey != "" {
		pipe.SRem(ctx, gatewayInstanceSIDsKey(instanceKey), sid)
	}
	if room == RoomAdmin {
		pipe.SRem(ctx, redisKeyGatewayAdminSIDs, sid)
	}
	for _, roomName := range joinedRooms {
		pipe.SRem(ctx, gatewayRoomMembersKey(roomName), sid)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	if room == RoomPublic && sessionID != "" {
		count, err := rdb.HIncrBy(ctx, redisKeyGatewayPublicSessionRefs, sessionID, -1).Result()
		if err != nil {
			return err
		}
		if count <= 0 {
			if err := rdb.HDel(ctx, redisKeyGatewayPublicSessionRefs, sessionID).Err(); err != nil {
				return err
			}
		}
	}

	for _, roomName := range joinedRooms {
		h.cleanupClusterRoomIfEmpty(ctx, roomName)
	}
	return nil
}

func (h *Hub) clusterJoinPublicRoom(sid, roomName string) bool {
	if !h.clusterStateEnabled() {
		return false
	}
	sid = strings.TrimSpace(sid)
	roomName = strings.TrimSpace(roomName)
	if sid == "" || roomName == "" {
		return false
	}

	ctx, cancel := h.clusterContext()
	defer cancel()

	pipe := h.rc.Raw().TxPipeline()
	pipe.SAdd(ctx, gatewaySIDRoomsKey(sid), roomName)
	pipe.SAdd(ctx, redisKeyGatewayPublicRooms, roomName)
	pipe.SAdd(ctx, gatewayRoomMembersKey(roomName), sid)
	if _, err := pipe.Exec(ctx); err != nil {
		h.clusterWarn("gateway room join sync failed", zap.String("sid", sid), zap.String("room", roomName), zap.Error(err))
		return false
	}
	return true
}

func (h *Hub) clusterLeavePublicRoom(sid, roomName string) bool {
	if !h.clusterStateEnabled() {
		return false
	}
	sid = strings.TrimSpace(sid)
	roomName = strings.TrimSpace(roomName)
	if sid == "" || roomName == "" {
		return false
	}

	ctx, cancel := h.clusterContext()
	defer cancel()

	pipe := h.rc.Raw().TxPipeline()
	pipe.SRem(ctx, gatewaySIDRoomsKey(sid), roomName)
	pipe.SRem(ctx, gatewayRoomMembersKey(roomName), sid)
	if _, err := pipe.Exec(ctx); err != nil {
		h.clusterWarn("gateway room leave sync failed", zap.String("sid", sid), zap.String("room", roomName), zap.Error(err))
		return false
	}
	h.cleanupClusterRoomIfEmpty(ctx, roomName)
	return true
}

func (h *Hub) cleanupClusterRoomIfEmpty(ctx context.Context, roomName string) {
	if !h.clusterStateEnabled() {
		return
	}
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return
	}

	count, err := h.rc.Raw().SCard(ctx, gatewayRoomMembersKey(roomName)).Result()
	if err != nil || count > 0 {
		if err != nil {
			h.clusterWarn("gateway room member count failed", zap.String("room", roomName), zap.Error(err))
		}
		return
	}

	pipe := h.rc.Raw().TxPipeline()
	pipe.Del(ctx, gatewayRoomMembersKey(roomName))
	pipe.SRem(ctx, redisKeyGatewayPublicRooms, roomName)
	_, _ = pipe.Exec(ctx)
}

func (h *Hub) clusterUpdateClientSession(sid, oldSessionID, newSessionID string) (int, bool) {
	if !h.clusterStateEnabled() {
		return 0, false
	}
	sid = strings.TrimSpace(sid)
	oldSessionID = normalizeSessionID(oldSessionID, sid)
	newSessionID = normalizeSessionID(newSessionID, sid)
	if sid == "" || newSessionID == "" {
		return 0, false
	}

	ctx, cancel := h.clusterContext()
	defer cancel()

	if err := h.rc.Raw().HSet(ctx, redisKeyGatewaySIDSession, sid, newSessionID).Err(); err != nil {
		h.clusterWarn("gateway session sync failed", zap.String("sid", sid), zap.Error(err))
		return 0, false
	}
	if oldSessionID != "" {
		count, err := h.rc.Raw().HIncrBy(ctx, redisKeyGatewayPublicSessionRefs, oldSessionID, -1).Result()
		if err != nil {
			h.clusterWarn("gateway old session decrement failed", zap.String("sid", sid), zap.String("session", oldSessionID), zap.Error(err))
			return 0, false
		}
		if count <= 0 {
			if err := h.rc.Raw().HDel(ctx, redisKeyGatewayPublicSessionRefs, oldSessionID).Err(); err != nil {
				h.clusterWarn("gateway old session delete failed", zap.String("sid", sid), zap.String("session", oldSessionID), zap.Error(err))
				return 0, false
			}
		}
	}
	if err := h.rc.Raw().HIncrBy(ctx, redisKeyGatewayPublicSessionRefs, newSessionID, 1).Err(); err != nil {
		h.clusterWarn("gateway new session increment failed", zap.String("sid", sid), zap.String("session", newSessionID), zap.Error(err))
		return 0, false
	}
	count, err := h.rc.Raw().HLen(ctx, redisKeyGatewayPublicSessionRefs).Result()
	if err != nil {
		h.clusterWarn("gateway public session count failed", zap.String("sid", sid), zap.Error(err))
		return 0, false
	}
	return int(count), true
}

func (h *Hub) clusterSetSIDIdentity(sid, identity string) bool {
	if !h.clusterStateEnabled() {
		return false
	}
	sid = strings.TrimSpace(sid)
	identity = strings.TrimSpace(identity)
	if sid == "" || identity == "" {
		return false
	}

	ctx, cancel := h.clusterContext()
	defer cancel()
	if err := h.rc.Raw().HSet(ctx, redisKeyGatewaySIDIdentity, sid, identity).Err(); err != nil {
		h.clusterWarn("gateway identity sync failed", zap.String("sid", sid), zap.Error(err))
		return false
	}
	return true
}

func (h *Hub) clusterIdentityOfSID(sid string) (string, bool) {
	if !h.clusterStateEnabled() {
		return "", false
	}
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return "", false
	}

	ctx, cancel := h.clusterContext()
	defer cancel()
	identity, err := h.rc.Raw().HGet(ctx, redisKeyGatewaySIDIdentity, sid).Result()
	if err != nil {
		if err != redis.Nil {
			h.clusterWarn("gateway identity lookup failed", zap.String("sid", sid), zap.Error(err))
		}
		return "", false
	}
	identity = strings.TrimSpace(identity)
	return identity, identity != ""
}

func (h *Hub) clusterJoinedRooms(sid string) ([]string, bool) {
	if !h.clusterStateEnabled() {
		return nil, false
	}
	ctx, cancel := h.clusterContext()
	defer cancel()
	rooms, err := h.clusterJoinedRoomsWithContext(ctx, sid)
	if err != nil {
		h.clusterWarn("gateway joined rooms lookup failed", zap.String("sid", sid), zap.Error(err))
		return nil, false
	}
	return rooms, true
}

func (h *Hub) clusterJoinedRoomsWithContext(ctx context.Context, sid string) ([]string, error) {
	if !h.clusterStateEnabled() {
		return nil, nil
	}
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return nil, nil
	}
	rooms, err := h.rc.Raw().SMembers(ctx, gatewaySIDRoomsKey(sid)).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	return filterNonEmptyStrings(rooms), nil
}

func (h *Hub) clusterPublicRoomCount() (map[string]int, bool) {
	if !h.clusterStateEnabled() {
		return nil, false
	}
	ctx, cancel := h.clusterContext()
	defer cancel()

	rooms, err := h.rc.Raw().SMembers(ctx, redisKeyGatewayPublicRooms).Result()
	if err != nil {
		h.clusterWarn("gateway room list lookup failed", zap.Error(err))
		return nil, false
	}
	rooms = filterNonEmptyStrings(rooms)
	if len(rooms) == 0 {
		return map[string]int{}, true
	}

	pipe := h.rc.Raw().Pipeline()
	cmds := make(map[string]*redis.IntCmd, len(rooms))
	for _, roomName := range rooms {
		cmds[roomName] = pipe.SCard(ctx, gatewayRoomMembersKey(roomName))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		h.clusterWarn("gateway room count refresh failed", zap.Error(err))
		return nil, false
	}

	out := make(map[string]int, len(rooms))
	staleRooms := make([]string, 0)
	for roomName, cmd := range cmds {
		count := int(cmd.Val())
		if count <= 0 {
			staleRooms = append(staleRooms, roomName)
			continue
		}
		out[roomName] = count
	}
	if len(staleRooms) > 0 {
		members := make([]interface{}, 0, len(staleRooms))
		for _, roomName := range staleRooms {
			members = append(members, roomName)
		}
		_ = h.rc.Raw().SRem(ctx, redisKeyGatewayPublicRooms, members...).Err()
	}
	return out, true
}

func (h *Hub) clusterClientCount(room string) (int, bool) {
	if !h.clusterStateEnabled() {
		return 0, false
	}
	ctx, cancel := h.clusterContext()
	defer cancel()

	var (
		count int64
		err   error
	)
	switch strings.TrimSpace(room) {
	case RoomPublic:
		count, err = h.rc.Raw().HLen(ctx, redisKeyGatewayPublicSessionRefs).Result()
	case RoomAdmin:
		count, err = h.rc.Raw().SCard(ctx, redisKeyGatewayAdminSIDs).Result()
	default:
		count, err = h.rc.Raw().HLen(ctx, redisKeyGatewaySIDRoom).Result()
	}
	if err != nil {
		h.clusterWarn("gateway client count lookup failed", zap.String("room", room), zap.Error(err))
		return 0, false
	}
	return int(count), true
}

func (h *Hub) clusterHasSID(sid string) (bool, bool) {
	if !h.clusterStateEnabled() {
		return false, false
	}
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return false, true
	}
	ctx, cancel := h.clusterContext()
	defer cancel()
	hasSID, err := h.rc.Raw().HExists(ctx, redisKeyGatewaySIDRoom, sid).Result()
	if err != nil {
		h.clusterWarn("gateway sid existence lookup failed", zap.String("sid", sid), zap.Error(err))
		return false, false
	}
	return hasSID, true
}

func (h *Hub) clusterSIDInPublicRoom(sid, roomName string) (bool, bool) {
	if !h.clusterStateEnabled() {
		return false, false
	}
	sid = strings.TrimSpace(sid)
	roomName = strings.TrimSpace(roomName)
	if sid == "" || roomName == "" {
		return false, true
	}
	ctx, cancel := h.clusterContext()
	defer cancel()
	inRoom, err := h.rc.Raw().SIsMember(ctx, gatewayRoomMembersKey(roomName), sid).Result()
	if err != nil {
		h.clusterWarn("gateway room membership lookup failed", zap.String("sid", sid), zap.String("room", roomName), zap.Error(err))
		return false, false
	}
	return inRoom, true
}

func (h *Hub) clusterRoomOfSID(ctx context.Context, sid string) (string, error) {
	if !h.clusterStateEnabled() {
		return "", nil
	}
	room, err := h.rc.Raw().HGet(ctx, redisKeyGatewaySIDRoom, strings.TrimSpace(sid)).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(room), nil
}

func (h *Hub) clusterSessionOfSID(ctx context.Context, sid string) (string, error) {
	if !h.clusterStateEnabled() {
		return "", nil
	}
	sessionID, err := h.rc.Raw().HGet(ctx, redisKeyGatewaySIDSession, strings.TrimSpace(sid)).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(sessionID), nil
}

func (h *Hub) clusterInstanceOfSID(ctx context.Context, sid string) (string, error) {
	if !h.clusterStateEnabled() {
		return "", nil
	}
	instanceKey, err := h.rc.Raw().HGet(ctx, redisKeyGatewaySIDInstance, strings.TrimSpace(sid)).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(instanceKey), nil
}

func filterNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
