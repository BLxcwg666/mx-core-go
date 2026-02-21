package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/modules/configs"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

const backupDir = "./backups"
const defaultS3PathTemplate = "backups/{Y}/{m}/{filename}"

// tableNames lists all tables to include in backups.
var tableNames = []string{
	"users", "posts", "notes", "pages", "categories", "topics",
	"comments", "says", "links", "recently", "snippets", "projects",
	"subscribers", "webhooks", "analyze_logs", "options", "drafts",
	"search_index", "ai_summaries", "ai_deep_readings",
}

// Handler handles backup/restore endpoints.
type Handler struct {
	db     *gorm.DB
	cfgSvc *configs.Service
}

func NewHandler(db *gorm.DB, cfgSvc *configs.Service) *Handler {
	return &Handler{db: db, cfgSvc: cfgSvc}
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

type backupItem struct {
	Filename string `json:"filename"`
	Size     string `json:"size"`
}

// GET /backups
func (h *Handler) list(c *gin.Context) {
	items := listBackups()
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func listBackups() []backupItem {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil
	}
	var items []backupItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, backupItem{
			Filename: e.Name(),
			Size:     formatSize(info.Size()),
		})
	}
	if items == nil {
		items = []backupItem{}
	}
	return items
}

func formatSize(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// GET /backups/new
func (h *Handler) createAndDownload(c *gin.Context) {
	buf, err := h.createBackupZip()
	if err != nil {
		response.InternalError(c, err)
		return
	}

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
}

// GET /backups/:filename
func (h *Handler) download(c *gin.Context) {
	filename := filepath.Base(c.Param("filename"))
	if !strings.HasSuffix(filename, ".zip") {
		response.BadRequest(c, "invalid filename")
		return
	}
	path := filepath.Join(backupDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			response.NotFound(c)
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

	if err := restoreFromZip(h.db, zr); err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"message": "restore successful"})
}

// PATCH /backups/rollback/:filename
func (h *Handler) rollback(c *gin.Context) {
	filename := filepath.Base(c.Param("filename"))
	path := filepath.Join(backupDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			response.NotFound(c)
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

	if err := restoreFromZip(h.db, zr); err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"message": "rollback successful"})
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
	_ = os.Remove(filepath.Join(backupDir, filename))
	response.NoContent(c)
}

type backupArtifact struct {
	Filename string
	Path     string
	Buffer   *bytes.Buffer
}

func (h *Handler) createLocalBackupArtifact(now time.Time) (*backupArtifact, error) {
	buf, err := h.createBackupZip()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, err
	}

	filename := fmt.Sprintf("backup-%s.zip", now.Format("2006-01-02T15-04-05"))
	path := filepath.Join(backupDir, filename)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	return &backupArtifact{
		Filename: filename,
		Path:     path,
		Buffer:   buf,
	}, nil
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
	if _, err := uploader.Upload(c.Request.Context(), key, artifact.Buffer.Bytes(), "application/zip"); err != nil {
		response.InternalError(c, err)
		return
	}

	response.NoContent(c)
}

func renderBackupObjectKey(template, filename string, now time.Time) string {
	tpl := strings.TrimSpace(template)
	if tpl == "" {
		tpl = defaultS3PathTemplate
	}

	replacer := strings.NewReplacer(
		"{Y}", now.Format("2006"),
		"{m}", now.Format("01"),
		"{d}", now.Format("02"),
		"{H}", now.Format("15"),
		"{M}", now.Format("04"),
		"{s}", now.Format("05"),
		"{filename}", filename,
	)

	key := replacer.Replace(tpl)
	key = strings.ReplaceAll(key, "\\", "/")
	key = strings.TrimSpace(strings.TrimPrefix(key, "/"))
	for strings.Contains(key, "//") {
		key = strings.ReplaceAll(key, "//", "/")
	}
	if key == "" {
		return filename
	}
	return key
}

// createBackupZip exports all tables as JSON into a ZIP archive.
func (h *Handler) createBackupZip() (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	for _, table := range tableNames {
		var rows []map[string]interface{}
		if err := h.db.Table(table).Find(&rows).Error; err != nil {
			continue
		}
		data, err := json.Marshal(rows)
		if err != nil {
			continue
		}
		f, err := w.Create(table + ".json")
		if err != nil {
			continue
		}
		f.Write(data)
	}

	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

// restoreFromZip imports JSON table dumps from a backup ZIP.
func restoreFromZip(db *gorm.DB, zr *zip.Reader) error {
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()

		name := f.Name
		name = name[:len(name)-5]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}

		var rows []map[string]interface{}
		if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 {
			continue
		}

		allowed := false
		for _, t := range tableNames {
			if name == t {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}

		db.Exec("DELETE FROM " + name)
		for _, row := range rows {
			db.Table(name).Create(row)
		}
	}
	return nil
}

// CreateLocalBackup creates a backup ZIP in the default backup directory.
func CreateLocalBackup(db *gorm.DB) error {
	h := &Handler{db: db}
	_, err := h.createLocalBackupArtifact(time.Now())
	return err
}
