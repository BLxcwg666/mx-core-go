package cron

import (
	"context"
	"fmt"
	"sync"
	"time"
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
	Status      JobStatus  `json:"status"`
	NextDate    *time.Time `json:"nextDate"`
	LastRunAt   *time.Time `json:"lastRunAt,omitempty"`
}

// TaskResult is returned when polling task execution status.
type TaskResult struct {
	Status  JobStatus `json:"status"` // "fulfill" | "reject" | "running" | "idle"
	Message string    `json:"message,omitempty"`
}

// Scheduler manages a collection of named cron jobs.
type Scheduler struct {
	mu   sync.RWMutex
	jobs map[string]*JobState
}

// New creates an empty Scheduler.
func New() *Scheduler {
	return &Scheduler{
		jobs: make(map[string]*JobState),
	}
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
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	js.mu.Unlock()

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
	js.mu.Unlock()
}

// Run manually triggers a job by name (non-blocking).
func (s *Scheduler) Run(ctx context.Context, name string) error {
	s.mu.RLock()
	js, ok := s.jobs[name]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("job %q not found", name)
	}
	go s.execute(ctx, js)
	return nil
}

// GetTask returns the current execution state of a job.
func (s *Scheduler) GetTask(name string) (*TaskResult, error) {
	s.mu.RLock()
	js, ok := s.jobs[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("job %q not found", name)
	}
	js.mu.Lock()
	defer js.mu.Unlock()
	return &TaskResult{Status: js.Status, Message: js.Message}, nil
}

// List returns a summary of all registered jobs.
func (s *Scheduler) List() []ListItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ListItem, 0, len(s.jobs))
	for _, js := range s.jobs {
		js.mu.Lock()
		next := js.NextRunAt
		items = append(items, ListItem{
			Name:        js.Name,
			Description: js.Description,
			Status:      js.Status,
			NextDate:    &next,
			LastRunAt:   js.LastRunAt,
		})
		js.mu.Unlock()
	}
	return items
}
