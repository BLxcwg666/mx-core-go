package pty

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const maxSessionRecords = 200

type SessionRecord struct {
	ID        string     `json:"id"`
	IP        string     `json:"ip"`
	StartTime time.Time  `json:"startTime"`
	EndTime   *time.Time `json:"endTime,omitempty"`
}

type sessionStore struct {
	mu      sync.RWMutex
	records []SessionRecord
}

func newSessionStore() *sessionStore {
	return &sessionStore{
		records: make([]SessionRecord, 0, 8),
	}
}

var globalStore = newSessionStore()

// StartSession records a PTY session start and returns session ID.
func StartSession(ip string) string {
	return globalStore.start(ip)
}

// EndSession marks a PTY session as finished.
func EndSession(sessionID string) {
	globalStore.end(sessionID)
}

// ListSessions returns session records ordered by start time descending.
func ListSessions() []SessionRecord {
	return globalStore.list()
}

func (s *sessionStore) start(ip string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.NewString()
	rec := SessionRecord{
		ID:        id,
		IP:        ip,
		StartTime: time.Now(),
	}

	s.records = append([]SessionRecord{rec}, s.records...)
	if len(s.records) > maxSessionRecords {
		s.records = s.records[:maxSessionRecords]
	}
	return id
}

func (s *sessionStore) end(sessionID string) {
	if sessionID == "" {
		return
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.records {
		if s.records[i].ID == sessionID {
			s.records[i].EndTime = &now
			return
		}
	}
}

func (s *sessionStore) list() []SessionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]SessionRecord, len(s.records))
	copy(out, s.records)
	return out
}
