package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	redisc "github.com/mx-space/core/internal/pkg/redis"
	redis "github.com/redis/go-redis/v9"
)

const (
	redisCronRunChannel = "mx:cron:run"
	redisCronTaskPrefix = "mx:cron:task:"
)

// JobStatus represents the last known state of a job.
type JobStatus string

const (
	StatusIdle    JobStatus = "idle"
	StatusRunning JobStatus = "running"
	StatusFulfill JobStatus = "fulfill"
	StatusReject  JobStatus = "reject"
)

// Job defines a scheduled background task.
type Job struct {
	Name        string
	Description string
	Interval    time.Duration
	Fn          func(ctx context.Context) error
}

// JobState holds runtime state for a registered job.
type JobState struct {
	Job
	Status    JobStatus
	Message   string
	LastRunAt *time.Time
	NextRunAt time.Time
	mu        sync.Mutex
}

// ListItem is the serializable representation of a job for the API.
type ListItem struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Enabled     bool       `json:"enabled"`
	Status      JobStatus  `json:"status"`
	NextDate    *time.Time `json:"nextDate"`
	LastRunAt   *time.Time `json:"lastRunAt,omitempty"`
}

// TaskResult is returned when polling task execution status.
type TaskResult struct {
	Enabled bool      `json:"enabled"`
	Status  JobStatus `json:"status"` // "fulfill" | "reject" | "running" | "idle"
	Message string    `json:"message,omitempty"`
}

type sharedTaskState struct {
	Status    JobStatus  `json:"status"`
	Message   string     `json:"message,omitempty"`
	LastRunAt *time.Time `json:"lastRunAt,omitempty"`
}

// Scheduler manages a collection of named cron jobs.
type Scheduler struct {
	mu      sync.RWMutex
	jobs    map[string]*JobState
	enabled bool
	rc      *redisc.Client
	baseCtx context.Context
}

// New creates an empty Scheduler.
func New() *Scheduler {
	return &Scheduler{
		jobs:    make(map[string]*JobState),
		enabled: true,
	}
}

func (s *Scheduler) SetEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
}

func (s *Scheduler) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

func (s *Scheduler) SetRedisClient(rc *redisc.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rc = rc
}

func (s *Scheduler) SetBaseContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baseCtx = ctx
}

// Register adds a job to the scheduler. Must be called before Start.
func (s *Scheduler) Register(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	next := now.Add(job.Interval)
	s.jobs[job.Name] = &JobState{
		Job:       job,
		Status:    StatusIdle,
		NextRunAt: next,
	}
}

// Start launches all registered jobs in background goroutines.
func (s *Scheduler) Start(ctx context.Context) {
	s.SetBaseContext(ctx)
	s.mu.RLock()
	if !s.enabled {
		s.mu.RUnlock()
		return
	}
	rc := s.rc
	defer s.mu.RUnlock()
	if rc != nil {
		go s.listenRunRequests(ctx)
	}
	for _, js := range s.jobs {
		go s.runLoop(ctx, js)
	}
}

func (s *Scheduler) runLoop(ctx context.Context, js *JobState) {
	for {
		wait := time.Until(js.NextRunAt)
		if wait < 0 {
			wait = 0
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			s.execute(ctx, js)
			js.mu.Lock()
			js.NextRunAt = time.Now().Add(js.Interval)
			js.mu.Unlock()
		}
	}
}

func (s *Scheduler) execute(ctx context.Context, js *JobState) {
	js.mu.Lock()
	if js.Status == StatusRunning {
		js.mu.Unlock()
		return
	}
	js.Status = StatusRunning
	js.Message = ""
	lastRunAt := js.LastRunAt
	js.mu.Unlock()
	s.persistTaskState(ctx, js.Name, StatusRunning, "", lastRunAt)

	now := time.Now()
	err := js.Fn(ctx)

	js.mu.Lock()
	js.LastRunAt = &now
	if err != nil {
		js.Status = StatusReject
		js.Message = err.Error()
	} else {
		js.Status = StatusFulfill
		js.Message = ""
	}
	status := js.Status
	message := js.Message
	lastRunAt = js.LastRunAt
	js.mu.Unlock()
	s.persistTaskState(ctx, js.Name, status, message, lastRunAt)
}

// Run manually triggers a job by name (non-blocking).
func (s *Scheduler) Run(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	s.mu.RLock()
	js, ok := s.jobs[name]
	enabled := s.enabled
	rc := s.rc
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("job %q not found", name)
	}
	if enabled {
		go s.execute(s.executionContext(ctx), js)
		return nil
	}
	if rc == nil {
		return fmt.Errorf("cron scheduler is disabled on this instance")
	}
	return rc.Publish(s.executionContext(ctx), redisCronRunChannel, name)
}

func (s *Scheduler) listenRunRequests(ctx context.Context) {
	s.mu.RLock()
	if !s.enabled || s.rc == nil {
		s.mu.RUnlock()
		return
	}
	rc := s.rc
	s.mu.RUnlock()

	sub := rc.Subscribe(ctx, redisCronRunChannel)
	defer sub.Close()
	ch := sub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			name := strings.TrimSpace(msg.Payload)
			if name == "" {
				continue
			}
			_ = s.Run(ctx, name)
		}
	}
}

// GetTask returns the current execution state of a job.
func (s *Scheduler) GetTask(name string) (*TaskResult, error) {
	name = strings.TrimSpace(name)
	s.mu.RLock()
	js, ok := s.jobs[name]
	enabled := s.enabled
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("job %q not found", name)
	}
	if shared, ok := s.loadTaskState(name); ok {
		return &TaskResult{Enabled: enabled, Status: shared.Status, Message: shared.Message}, nil
	}
	js.mu.Lock()
	defer js.mu.Unlock()
	return &TaskResult{Enabled: enabled, Status: js.Status, Message: js.Message}, nil
}

// List returns a summary of all registered jobs.
func (s *Scheduler) List() []ListItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enabled := s.enabled
	items := make([]ListItem, 0, len(s.jobs))
	for _, js := range s.jobs {
		js.mu.Lock()
		var next *time.Time
		if enabled {
			nextDate := js.NextRunAt
			next = &nextDate
		}
		item := ListItem{
			Name:        js.Name,
			Description: js.Description,
			Enabled:     enabled,
			Status:      js.Status,
			NextDate:    next,
			LastRunAt:   js.LastRunAt,
		}
		js.mu.Unlock()
		if shared, ok := s.loadTaskState(js.Name); ok {
			item.Status = shared.Status
			item.LastRunAt = shared.LastRunAt
		}
		items = append(items, item)
	}
	return items
}

func (s *Scheduler) executionContext(ctx context.Context) context.Context {
	s.mu.RLock()
	baseCtx := s.baseCtx
	s.mu.RUnlock()
	if baseCtx != nil {
		return baseCtx
	}
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func (s *Scheduler) persistTaskState(ctx context.Context, name string, status JobStatus, message string, lastRunAt *time.Time) {
	s.mu.RLock()
	rc := s.rc
	s.mu.RUnlock()
	if rc == nil {
		return
	}
	payload, err := json.Marshal(sharedTaskState{
		Status:    status,
		Message:   message,
		LastRunAt: lastRunAt,
	})
	if err != nil {
		return
	}
	ttl := time.Duration(0)
	if status == StatusRunning {
		ttl = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(s.executionContext(ctx), 5*time.Second)
	defer cancel()
	_ = rc.Set(runCtx, redisCronTaskPrefix+name, payload, ttl)
}

func (s *Scheduler) loadTaskState(name string) (*sharedTaskState, bool) {
	s.mu.RLock()
	rc := s.rc
	s.mu.RUnlock()
	if rc == nil {
		return nil, false
	}
	runCtx, cancel := context.WithTimeout(s.executionContext(nil), 5*time.Second)
	defer cancel()
	data, err := rc.Raw().Get(runCtx, redisCronTaskPrefix+name).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, false
		}
		return nil, false
	}
	var state sharedTaskState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false
	}
	return &state, true
}
