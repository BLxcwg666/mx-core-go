package crontask

import (
	"encoding/json"
	"strconv"

	"github.com/gin-gonic/gin"
	pkgcron "github.com/mx-space/core/internal/pkg/cron"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"github.com/mx-space/core/internal/pkg/taskqueue"
)

// Handler wraps the scheduler for HTTP access.
type Handler struct {
	sched   *pkgcron.Scheduler
	taskSvc *taskqueue.Service
}

func NewHandler(sched *pkgcron.Scheduler, taskSvc *taskqueue.Service) *Handler {
	return &Handler{sched: sched, taskSvc: taskSvc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/cron-task", authMW)
	g.GET("", h.list)
	g.GET("/:name", h.get)
	g.POST("/:name/run", h.run)

	tasks := g.Group("/tasks")
	tasks.GET("", h.listTasks)
	tasks.GET("/:taskId", h.getTask)
	tasks.POST("/:taskId/cancel", h.cancelTask)
	tasks.POST("/:taskId/retry", h.retryTask)
	tasks.DELETE("/:taskId", h.deleteTask)
	tasks.DELETE("", h.deleteTasks)
}

// GET /cron-task — list all jobs
func (h *Handler) list(c *gin.Context) {
	response.OK(c, h.sched.List())
}

// GET /cron-task/:name — get single job status
func (h *Handler) get(c *gin.Context) {
	result, err := h.sched.GetTask(c.Param("name"))
	if err != nil {
		response.NotFoundMsg(c, "定时任务不存在")
		return
	}
	response.OK(c, result)
}

// POST /cron-task/:name/run — manually trigger a job
func (h *Handler) run(c *gin.Context) {
	if err := h.sched.Run(c.Request.Context(), c.Param("name")); err != nil {
		response.NotFoundMsg(c, "定时任务不存在")
		return
	}
	response.OK(c, gin.H{"message": "job triggered"})
}

// GET /cron-task/tasks
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

	tasks, total, err := h.taskSvc.List(c.Request.Context(), q.Page, q.Size, taskTypePtr, statusPtr)
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

// GET /cron-task/tasks/:taskId
func (h *Handler) getTask(c *gin.Context) {
	task, err := h.taskSvc.GetByID(c.Request.Context(), c.Param("taskId"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if task == nil {
		response.NotFoundMsg(c, "任务不存在")
		return
	}
	response.OK(c, task)
}

// POST /cron-task/tasks/:taskId/cancel
func (h *Handler) cancelTask(c *gin.Context) {
	if err := h.taskSvc.Cancel(c.Request.Context(), c.Param("taskId")); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.NoContent(c)
}

// POST /cron-task/tasks/:taskId/retry
func (h *Handler) retryTask(c *gin.Context) {
	task, err := h.taskSvc.GetByID(c.Request.Context(), c.Param("taskId"))
	if err != nil || task == nil {
		response.NotFoundMsg(c, "任务不存在")
		return
	}
	// Re-enqueue with same type + payload, clearing dedupKey
	var rawPayload interface{}
	if err := json.Unmarshal(task.Payload, &rawPayload); err != nil {
		response.BadRequest(c, "invalid task payload")
		return
	}
	newTask, err := h.taskSvc.Enqueue(c.Request.Context(), task.Type, rawPayload, "", task.GroupKey)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, newTask)
}

// DELETE /cron-task/tasks/:taskId
func (h *Handler) deleteTask(c *gin.Context) {
	if err := h.taskSvc.DeleteByID(c.Request.Context(), c.Param("taskId")); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.NoContent(c)
}

// DELETE /cron-task/tasks?status=...&type=...&before=<unix_ms>
func (h *Handler) deleteTasks(c *gin.Context) {
	beforeStr := c.Query("before")
	var before int64
	if beforeStr != "" {
		if v, err := strconv.ParseInt(beforeStr, 10, 64); err == nil {
			before = v
		}
	}
	if err := h.taskSvc.DeleteCompleted(c.Request.Context(), before); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}
