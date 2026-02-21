package taskqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	redisc "github.com/mx-space/core/internal/pkg/redis"
	"github.com/redis/go-redis/v9"
)

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

// Task is a unit of background work stored in Redis.
type Task struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Status    TaskStatus      `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	DedupKey  string          `json:"dedup_key,omitempty"`
	GroupKey  string          `json:"group_key,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

const (
	keyPrefix   = "mx:task:"
	keyIndex    = "mx:tasks:index"   // sorted set: score=created_at, member=task_id
	keyDedupSet = "mx:tasks:dedup:"  // hash: dedup_key -> task_id
	taskTTL     = 7 * 24 * time.Hour // tasks expire after 7 days
)

// Service manages the Redis-backed task queue.
type Service struct {
	rc *redisc.Client
}

func NewService(rc *redisc.Client) *Service {
	return &Service{rc: rc}
}

func (s *Service) taskKey(id string) string { return keyPrefix + id }

// Enqueue creates a new task, respecting deduplication.
func (s *Service) Enqueue(ctx context.Context, taskType string, payload interface{}, dedupKey, groupKey string) (*Task, error) {
	if dedupKey != "" {
		existing, err := s.rc.Raw().HGet(ctx, keyDedupSet+taskType, dedupKey).Result()
		if err == nil && existing != "" {
			return s.GetByID(ctx, existing)
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	task := &Task{
		ID:        uuid.New().String(),
		Type:      taskType,
		Payload:   payloadBytes,
		Status:    TaskPending,
		DedupKey:  dedupKey,
		GroupKey:  groupKey,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	data, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}

	pipe := s.rc.Raw().TxPipeline()
	pipe.Set(ctx, s.taskKey(task.ID), data, taskTTL)
	pipe.ZAdd(ctx, keyIndex, redis.Z{
		Score:  float64(task.CreatedAt.UnixMilli()),
		Member: task.ID,
	})
	if dedupKey != "" {
		pipe.HSet(ctx, keyDedupSet+taskType, dedupKey, task.ID)
		pipe.Expire(ctx, keyDedupSet+taskType, taskTTL)
	}
	_, err = pipe.Exec(ctx)
	return task, err
}

// GetByID retrieves a task by its ID.
func (s *Service) GetByID(ctx context.Context, id string) (*Task, error) {
	data, err := s.rc.Raw().Get(ctx, s.taskKey(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var task Task
	return &task, json.Unmarshal(data, &task)
}

// UpdateStatus sets a task's status and optional result/error.
func (s *Service) UpdateStatus(ctx context.Context, id string, status TaskStatus, result interface{}, errMsg string) error {
	task, err := s.GetByID(ctx, id)
	if err != nil || task == nil {
		return fmt.Errorf("task not found")
	}

	task.Status = status
	task.UpdatedAt = time.Now()
	task.Error = errMsg

	if result != nil {
		task.Result, _ = json.Marshal(result)
	}

	if (status == TaskCompleted || status == TaskFailed || status == TaskCancelled) && task.DedupKey != "" {
		s.rc.Raw().HDel(ctx, keyDedupSet+task.Type, task.DedupKey)
	}

	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return s.rc.Raw().Set(ctx, s.taskKey(id), data, taskTTL).Err()
}

// List returns tasks matching optional filters, ordered by creation time descending.
func (s *Service) List(ctx context.Context, page, size int, taskType *string, status *TaskStatus) ([]*Task, int64, error) {
	ids, err := s.rc.Raw().ZRevRange(ctx, keyIndex, 0, -1).Result()
	if err != nil {
		return nil, 0, err
	}

	var tasks []*Task
	for _, id := range ids {
		task, err := s.GetByID(ctx, id)
		if err != nil || task == nil {
			continue
		}
		if taskType != nil && task.Type != *taskType {
			continue
		}
		if status != nil && task.Status != *status {
			continue
		}
		tasks = append(tasks, task)
	}

	total := int64(len(tasks))
	start := (page - 1) * size
	end := start + size
	if start >= len(tasks) {
		return []*Task{}, total, nil
	}
	if end > len(tasks) {
		end = len(tasks)
	}
	return tasks[start:end], total, nil
}

// Cancel marks a task as cancelled if it is still pending.
func (s *Service) Cancel(ctx context.Context, id string) error {
	task, err := s.GetByID(ctx, id)
	if err != nil || task == nil {
		return fmt.Errorf("task not found")
	}
	if task.Status != TaskPending {
		return fmt.Errorf("can only cancel pending tasks")
	}
	return s.UpdateStatus(ctx, id, TaskCancelled, nil, "cancelled by user")
}

// DeleteByID removes a single task by ID.
func (s *Service) DeleteByID(ctx context.Context, id string) error {
	task, err := s.GetByID(ctx, id)
	if err != nil || task == nil {
		return fmt.Errorf("task not found")
	}
	pipe := s.rc.Raw().TxPipeline()
	pipe.Del(ctx, s.taskKey(id))
	pipe.ZRem(ctx, keyIndex, id)
	if task.DedupKey != "" {
		pipe.HDel(ctx, keyDedupSet+task.Type, task.DedupKey)
	}
	_, err = pipe.Exec(ctx)
	return err
}

// DeleteCompleted removes completed/failed/cancelled tasks.
func (s *Service) DeleteCompleted(ctx context.Context, beforeMS int64) error {
	ids, _ := s.rc.Raw().ZRange(ctx, keyIndex, 0, -1).Result()
	pipe := s.rc.Raw().TxPipeline()
	for _, id := range ids {
		task, err := s.GetByID(ctx, id)
		if err != nil || task == nil {
			continue
		}
		if task.Status != TaskCompleted && task.Status != TaskFailed && task.Status != TaskCancelled {
			continue
		}
		if beforeMS > 0 && task.CreatedAt.UnixMilli() >= beforeMS {
			continue
		}
		pipe.Del(ctx, s.taskKey(id))
		pipe.ZRem(ctx, keyIndex, id)
		if task.DedupKey != "" {
			pipe.HDel(ctx, keyDedupSet+task.Type, task.DedupKey)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}
