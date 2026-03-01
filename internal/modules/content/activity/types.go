package activity

import "sync"

const (
	activityTypeLike         = 0
	activityTypeReadDuration = 1
)

type likeDTO struct {
	ID   string `json:"id" binding:"required"`
	Type string `json:"type" binding:"required"`
}

type presenceUpdateDTO struct {
	Identity    string `json:"identity" binding:"required"`
	Position    int    `json:"position"`
	RoomName    string `json:"roomName" binding:"required"`
	SID         string `json:"sid" binding:"required"`
	TS          int64  `json:"ts"`
	DisplayName string `json:"displayName"`
	ReaderID    string `json:"readerId"`
}

type getPresenceQuery struct {
	RoomName string `form:"room_name"`
}

type presenceRecord struct {
	Identity      string
	RoomName      string
	Position      int
	SID           string
	DisplayName   string
	ReaderID      string
	OperationTime int64
	UpdatedAt     int64
	ConnectedAt   int64
	JoinedAt      int64
	IP            string
}

var presenceStore = struct {
	mu    sync.RWMutex
	rooms map[string]map[string]presenceRecord // room -> sid -> record
}{
	rooms: map[string]map[string]presenceRecord{},
}
