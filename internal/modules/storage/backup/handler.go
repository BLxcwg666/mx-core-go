package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/modules/gateway/gateway"
	"github.com/mx-space/core/internal/modules/gateway/webhook"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"github.com/mx-space/core/internal/pkg/response"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func NewHandler(db *gorm.DB, cfgSvc *configs.Service, rc *pkgredis.Client, opts ...HandlerOption) *Handler {
	h := &Handler{db: db, cfgSvc: cfgSvc, rc: rc, logger: zap.NewNop()}
	for _, o := range opts {
		o(h)
	}
	return h
}

// HandlerOption configures a backup Handler.
type HandlerOption func(*Handler)

// WithLogger sets the logger for the backup handler.
func WithLogger(l *zap.Logger) HandlerOption {
	return func(h *Handler) {
		if l != nil {
			h.logger = l.Named("BackupService")
		}
	}
}

// WithWebhook enables public cache refresh after restoring a backup.
func WithWebhook(svc *webhook.Service) HandlerOption {
	return func(h *Handler) {
		h.webhook = svc
	}
}

// WithHub lets a restore tell connected admin panels and sites to reload.
func WithHub(hub *gateway.Hub) HandlerOption {
	return func(h *Handler) {
		h.hub = hub
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/backups", authMW)

	g.GET("", h.list)
	g.GET("/new", h.createAndDownload)
	g.POST("/new", h.create)
	g.GET("/:filename", h.download)
	g.POST("", h.uploadAndRestore)
	g.POST("/rollback", h.uploadAndRestore)
	g.POST("/upload-to-s3", h.uploadToS3)
	g.PATCH("/rollback/:filename", h.rollback)
	g.PATCH("/:filename", h.rollback)
	g.DELETE("", h.delete)
	g.DELETE("/:filename", h.deleteOne)
}

// GET /backups
func (h *Handler) list(c *gin.Context) {
	items := listBackups()
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// GET /backups/new
// The S3 upload outcome is reported via X-Backup-S3-Status / X-Backup-S3-Error since the body is the ZIP itself.
func (h *Handler) createAndDownload(c *gin.Context) {
	// The upload should finish even if the admin page is closed mid-request.
	result, artifact, err := h.createBackup(context.WithoutCancel(c.Request.Context()))
	if err != nil {
		response.InternalError(c, err)
		return
	}

	c.Header("X-Backup-S3-Status", result.S3.Status)
	if result.S3.Error != "" {
		c.Header("X-Backup-S3-Error", url.QueryEscape(result.S3.Error))
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, result.Filename))
	c.Header("Content-Type", "application/zip")
	c.File(artifact.Path)
}

// POST /backups/new
// Responds 200 with the S3 outcome in the body: a failed upload still leaves a usable local backup.
func (h *Handler) create(c *gin.Context) {
	result, _, err := h.createBackup(context.WithoutCancel(c.Request.Context()))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, result)
}

// GET /backups/:filename
func (h *Handler) download(c *gin.Context) {
	filename := filepath.Base(c.Param("filename"))
	if !strings.HasSuffix(filename, ".zip") {
		response.BadRequest(c, "invalid filename")
		return
	}
	backupDir := resolveBackupDir()
	path := filepath.Join(backupDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			response.NotFoundMsg(c, "文件不存在")
			return
		}
		response.InternalError(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/zip", data)
}

// POST /backups/rollback
func (h *Handler) uploadAndRestore(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "missing file")
		return
	}

	src, err := file.Open()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	defer src.Close()

	data, err := io.ReadAll(src)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		response.BadRequest(c, "invalid zip file")
		return
	}

	if err := RestoreFromZip(h.db, zr); err != nil {
		h.logger.Warn("数据恢复失败", zap.Error(err))
		response.InternalError(c, err)
		return
	}
	h.invalidateRuntimeCaches(c)
	h.dispatchContentRefresh()
	h.logger.Info("数据恢复成功（上传文件）")
	response.OK(c, gin.H{"message": "restore successful"})
}

// PATCH /backups/rollback/:filename
func (h *Handler) rollback(c *gin.Context) {
	filename := filepath.Base(c.Param("filename"))
	backupDir := resolveBackupDir()
	path := filepath.Join(backupDir, filename)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			response.NotFoundMsg(c, "文件不存在")
			return
		}
		response.InternalError(c, err)
		return
	}

	// Read the archive from disk instead of loading it whole: it includes the uploaded files.
	zr, err := zip.OpenReader(path)
	if err != nil {
		response.BadRequest(c, "invalid zip file")
		return
	}
	defer zr.Close()

	h.logger.Info(fmt.Sprintf("回滚备份：%s", filename))
	if err := RestoreFromZip(h.db, &zr.Reader); err != nil {
		h.logger.Warn("回滚失败", zap.Error(err))
		response.InternalError(c, err)
		return
	}
	h.invalidateRuntimeCaches(c)
	h.dispatchContentRefresh()
	h.logger.Info("回滚成功")
	response.OK(c, gin.H{"message": "rollback successful"})
}

func (h *Handler) invalidateRuntimeCaches(c *gin.Context) {
	if h.cfgSvc != nil {
		h.cfgSvc.Invalidate()
	}
	_ = h.rc.Raw().FlushDB(c.Request.Context())
}

func (h *Handler) dispatchContentRefresh() {
	if h.webhook != nil {
		h.webhook.DispatchContentRefresh("backup-restore")
	}
	if h.hub != nil {
		h.hub.Broadcast("CONTENT_REFRESH", nil, "")
	}
}

// DELETE /backups
func (h *Handler) delete(c *gin.Context) {
	files := strings.TrimSpace(c.Query("files"))

	var body struct {
		Files string `json:"files"`
	}
	if files == "" {
		_ = c.ShouldBindJSON(&body)
		files = strings.TrimSpace(body.Files)
	}
	if files == "" {
		response.BadRequest(c, "missing files")
		return
	}

	backupDir := resolveBackupDir()
	filenames := strings.Split(files, ",")
	for _, name := range filenames {
		name = strings.TrimSpace(filepath.Base(name))
		if name == "" || !strings.HasSuffix(name, ".zip") {
			continue
		}
		os.Remove(filepath.Join(backupDir, name))
	}
	response.NoContent(c)
}

func (h *Handler) deleteOne(c *gin.Context) {
	filename := strings.TrimSpace(filepath.Base(c.Param("filename")))
	if filename == "" || !strings.HasSuffix(filename, ".zip") {
		response.BadRequest(c, "invalid filename")
		return
	}
	backupDir := resolveBackupDir()
	_ = os.Remove(filepath.Join(backupDir, filename))
	response.NoContent(c)
}

// POST /backups/upload-to-s3
func (h *Handler) uploadToS3(c *gin.Context) {
	cfg, err := h.loadConfig()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if !cfg.BackupOptions.Enable {
		// Keep compatibility: backup disabled means no-op.
		response.NoContent(c)
		return
	}

	result, _, err := h.createBackup(context.WithoutCancel(c.Request.Context()))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if result.S3.Status == S3UploadFailed {
		response.InternalError(c, errors.New(result.S3.Error))
		return
	}
	response.NoContent(c)
}
