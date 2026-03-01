package backup

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/backups", authMW)

	g.GET("", h.list)
	g.GET("/new", h.createAndDownload)
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
func (h *Handler) createAndDownload(c *gin.Context) {
	h.logger.Info("备份数据库中...")
	buf, err := h.createBackupZip()
	if err != nil {
		h.logger.Warn("备份失败", zap.Error(err))
		response.InternalError(c, err)
		return
	}

	backupDir := resolveBackupDir()
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		response.InternalError(c, err)
		return
	}
	filename := fmt.Sprintf("backup-%s.zip", time.Now().Format("2006-01-02T15-04-05"))
	path := filepath.Join(backupDir, filename)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		response.InternalError(c, err)
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
	h.logger.Info(fmt.Sprintf("备份成功：%s", filename))
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
	h.logger.Info("数据恢复成功（上传文件）")
	response.OK(c, gin.H{"message": "restore successful"})
}

// PATCH /backups/rollback/:filename
func (h *Handler) rollback(c *gin.Context) {
	filename := filepath.Base(c.Param("filename"))
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

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		response.BadRequest(c, "invalid zip file")
		return
	}

	h.logger.Info(fmt.Sprintf("回滚备份：%s", filename))
	if err := RestoreFromZip(h.db, zr); err != nil {
		h.logger.Warn("回滚失败", zap.Error(err))
		response.InternalError(c, err)
		return
	}
	h.invalidateRuntimeCaches(c)
	h.logger.Info("回滚成功")
	response.OK(c, gin.H{"message": "rollback successful"})
}

func (h *Handler) invalidateRuntimeCaches(c *gin.Context) {
	if h.cfgSvc != nil {
		h.cfgSvc.Invalidate()
	}
	_ = h.rc.Raw().FlushDB(c.Request.Context())
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
	if h.cfgSvc == nil {
		response.InternalError(c, fmt.Errorf("config service is unavailable"))
		return
	}

	cfg, err := h.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cfg == nil {
		response.InternalError(c, fmt.Errorf("configs not initialized"))
		return
	}
	if !cfg.BackupOptions.Enable {
		// Keep compatibility: backup disabled means no-op.
		response.NoContent(c)
		return
	}

	uploader, err := newS3Uploader(cfg.S3Options)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	now := time.Now()
	artifact, err := h.createLocalBackupArtifact(now)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	key := renderBackupObjectKey(cfg.BackupOptions.Path, artifact.Filename, now)
	h.logger.Info(fmt.Sprintf("上传备份到 S3：%s", key))
	if _, err := uploader.Upload(c.Request.Context(), key, artifact.Buffer.Bytes(), "application/zip"); err != nil {
		h.logger.Warn("S3 上传失败", zap.Error(err))
		response.InternalError(c, err)
		return
	}

	h.logger.Info("S3 上传成功")
	response.NoContent(c)
}
