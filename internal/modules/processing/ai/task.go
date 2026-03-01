package ai

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"github.com/mx-space/core/internal/pkg/taskqueue"
	"gorm.io/gorm"
)

// GET /ai/tasks  [auth]
func (h *Handler) listTasks(c *gin.Context) {
	q := pagination.FromContext(c)
	taskType := c.Query("type")
	statusStr := c.Query("status")

	var taskTypePtr *string
	var statusPtr *taskqueue.TaskStatus

	if taskType != "" {
		taskTypePtr = &taskType
	}
	if statusStr != "" {
		s := taskqueue.TaskStatus(statusStr)
		statusPtr = &s
	}

	tasks, total, err := h.svc.taskSvc.List(c.Request.Context(), q.Page, q.Size, taskTypePtr, statusPtr)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	totalPages := int((total + int64(q.Size) - 1) / int64(q.Size))
	response.Paged(c, tasks, response.Pagination{
		Total:       total,
		CurrentPage: q.Page,
		TotalPage:   totalPages,
		Size:        q.Size,
		HasNextPage: q.Page < totalPages,
	})
}

// GET /ai/tasks/:id  [auth]
func (h *Handler) getTask(c *gin.Context) {
	task, err := h.svc.taskSvc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if task == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, task)
}

// DELETE /ai/tasks/:id  [auth]
func (h *Handler) deleteTask(c *gin.Context) {
	if err := h.svc.taskSvc.DeleteByID(c.Request.Context(), c.Param("id")); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.NoContent(c)
}

// DELETE /ai/tasks?before=<unix_ms>  [auth]
func (h *Handler) batchDeleteTasks(c *gin.Context) {
	beforeStr := c.Query("before")
	var before int64
	if beforeStr != "" {
		if v, err := strconv.ParseInt(beforeStr, 10, 64); err == nil {
			before = v
		}
	}
	if err := h.svc.taskSvc.DeleteCompleted(c.Request.Context(), before); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

// POST /ai/tasks/:id/cancel  [auth]
func (h *Handler) cancelTask(c *gin.Context) {
	task, err := h.svc.taskSvc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if task == nil {
		response.NotFound(c)
		return
	}
	if task.Status == taskqueue.TaskCompleted ||
		task.Status == taskqueue.TaskFailed ||
		task.Status == taskqueue.TaskCancelled {
		response.BadRequest(c, "AI 任务已完成，无法取消")
		return
	}
	if task.Status == taskqueue.TaskRunning {
		if err := h.svc.taskSvc.UpdateStatus(c.Request.Context(), task.ID, taskqueue.TaskCancelled, nil, "cancelled by user"); err != nil {
			response.InternalError(c, err)
			return
		}
		response.NoContent(c)
		return
	}
	if err := h.svc.taskSvc.Cancel(c.Request.Context(), task.ID); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.NoContent(c)
}

// POST /ai/tasks/:id/retry  [auth]
func (h *Handler) retryTask(c *gin.Context) {
	task, err := h.svc.taskSvc.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil || task == nil {
		response.NotFound(c)
		return
	}
	if task.Status != taskqueue.TaskFailed && task.Status != taskqueue.TaskCancelled {
		response.BadRequest(c, "AI 任务无法重试")
		return
	}

	var payload SummaryPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		response.BadRequest(c, "invalid task payload")
		return
	}

	newTask, err := h.svc.EnqueueSummary(c.Request.Context(), payload.RefID, payload.RefType, payload.Title, payload.Lang)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, newTask)
}

// POST /ai/summaries/task  [auth]
func (h *Handler) createSummaryTask(c *gin.Context) {
	var dto createSummaryTaskDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	refID := strings.TrimSpace(dto.RefID)
	if refID == "" {
		refID = strings.TrimSpace(dto.RefIDLegacy)
	}
	if refID == "" {
		response.BadRequest(c, "refId is required")
		return
	}

	task, err := h.svc.EnqueueSummary(c.Request.Context(), refID, "", "", strings.TrimSpace(dto.Lang))
	if err != nil {
		if errors.Is(err, errSummaryArticleNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	response.Created(c, task)
}

// GET /ai/summaries/task?ref_id=&lang=  [auth]
func (h *Handler) getSummaryTask(c *gin.Context) {
	refID := strings.TrimSpace(c.Query("ref_id"))
	lang := strings.TrimSpace(c.Query("lang"))
	if lang == "" {
		lang = "default"
	}
	if refID == "" {
		response.BadRequest(c, "ref_id is required")
		return
	}

	dedupKey := refID + ":" + lang
	tasks, _, err := h.svc.taskSvc.List(c.Request.Context(), 1, 100, strPtr(TaskTypeSummary), nil)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	for _, t := range tasks {
		if t.DedupKey == dedupKey {
			response.OK(c, t)
			return
		}
	}
	response.NotFound(c)
}

func strPtr(s string) *string { return &s }

// GET /ai/tasks/group/:groupKey  [auth]
func (h *Handler) getTasksByGroup(c *gin.Context) {
	groupKey := c.Param("groupKey")
	if groupKey == "" {
		groupKey = c.Param("id")
	}
	if groupKey == "" {
		response.BadRequest(c, "group id is required")
		return
	}
	q := pagination.FromContext(c)

	all, _, err := h.svc.taskSvc.List(c.Request.Context(), 1, 1000, nil, nil)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	var filtered []*taskqueue.Task
	for _, t := range all {
		if t.GroupKey == groupKey {
			filtered = append(filtered, t)
		}
	}

	total := int64(len(filtered))
	start := (q.Page - 1) * q.Size
	end := start + q.Size
	if start >= len(filtered) {
		filtered = []*taskqueue.Task{}
	} else {
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[start:end]
	}

	totalPages := int((total + int64(q.Size) - 1) / int64(q.Size))
	response.Paged(c, filtered, response.Pagination{
		Total:       total,
		CurrentPage: q.Page,
		TotalPage:   totalPages,
		Size:        q.Size,
		HasNextPage: q.Page < totalPages,
	})
}

// DELETE /ai/tasks/group/:groupKey  [auth]
func (h *Handler) cancelTasksByGroup(c *gin.Context) {
	groupKey := c.Param("groupKey")
	if groupKey == "" {
		groupKey = c.Param("id")
	}
	if groupKey == "" {
		response.BadRequest(c, "group id is required")
		return
	}

	all, _, err := h.svc.taskSvc.List(c.Request.Context(), 1, 1000, nil, nil)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	cancelled := 0
	for _, t := range all {
		if t.GroupKey != groupKey {
			continue
		}
		switch t.Status {
		case taskqueue.TaskPending:
			if err := h.svc.taskSvc.Cancel(c.Request.Context(), t.ID); err == nil {
				cancelled++
			}
		case taskqueue.TaskRunning:
			if err := h.svc.taskSvc.UpdateStatus(c.Request.Context(), t.ID, taskqueue.TaskCancelled, nil, "cancelled by group"); err == nil {
				cancelled++
			}
		}
	}

	response.OK(c, gin.H{"cancelled": cancelled})
}
