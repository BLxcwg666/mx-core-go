package file

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/storage/backup"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

// Handler manages file upload, retrieval, deletion, and orphan cleanup.
type Handler struct {
	db        *gorm.DB
	cfgSvc    *appconfigs.Service
	staticDir string
}

func NewHandler(db *gorm.DB, cfgSvc ...*appconfigs.Service) *Handler {
	var service *appconfigs.Service
	if len(cfgSvc) > 0 {
		service = cfgSvc[0]
	}
	return &Handler{
		db:        db,
		cfgSvc:    service,
		staticDir: resolveStaticDir(),
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	for _, prefix := range []string{"/objects", "/files"} {
		g := rg.Group(prefix)

		g.DELETE("/orphans/batch", authMW, h.batchDeleteOrphans)
		g.POST("/s3/batch-upload", authMW, h.batchUploadToS3)
		g.GET("/orphans/list", authMW, h.listOrphans)
		g.GET("/orphans/count", authMW, h.countOrphans)
		g.POST("/orphans/cleanup", authMW, h.cleanupOrphans)

		g.POST("/upload", authMW, h.upload)
		g.GET("/:type", authMW, h.listByType)
		g.GET("/:type/:name", h.get)
		g.DELETE("/:type/:name", authMW, h.delete)
		g.PATCH("/:type/:name/rename", authMW, h.rename)
	}
}

func (h *Handler) listByType(c *gin.Context) {
	typ := normalizeType(c.Param("type"))
	if typ == "" {
		response.BadRequest(c, "invalid file type")
		return
	}

	dir := filepath.Join(h.staticDir, typ)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			response.OK(c, []gin.H{})
			return
		}
		response.InternalError(c, err)
		return
	}

	root := detectRoot(c.Request.URL.Path)
	items := make([]gin.H, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		items = append(items, gin.H{
			"name":    ent.Name(),
			"url":     h.resolveURL(c, root, typ, ent.Name()),
			"created": info.ModTime().UnixMilli(),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i]["created"].(int64) > items[j]["created"].(int64)
	})
	response.OK(c, items)
}

func (h *Handler) get(c *gin.Context) {
	typ := normalizeType(c.Param("type"))
	name := safeName(c.Param("name"))
	if typ == "" || name == "" {
		response.NotFound(c)
		return
	}

	path := filepath.Join(h.staticDir, typ, name)
	if _, err := os.Stat(path); err != nil {
		response.NotFound(c)
		return
	}

	c.Header("Cache-Control", "public, max-age=31536000")
	c.File(path)
}

func (h *Handler) upload(c *gin.Context) {
	typ := normalizeTypeDefault(c.Query("type"), "file")
	if typ == "" {
		response.BadRequest(c, "invalid file type")
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file is required")
		return
	}

	if isImageType(typ) {
		cfg, err := h.loadConfig()
		if err != nil {
			response.InternalError(c, err)
			return
		}
		if cfg != nil && cfg.ImageBedOptions.Enable {
			if err := validateImageBedFile(fileHeader.Filename, fileHeader.Size, cfg.ImageBedOptions.AllowedFormats, cfg.ImageBedOptions.MaxSizeMB); err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			file, err := fileHeader.Open()
			if err != nil {
				response.InternalError(c, err)
				return
			}
			defer file.Close()
			payload, err := io.ReadAll(file)
			if err != nil {
				response.InternalError(c, err)
				return
			}
			if err := validateImageBedFile(fileHeader.Filename, int64(len(payload)), cfg.ImageBedOptions.AllowedFormats, cfg.ImageBedOptions.MaxSizeMB); err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			uploader, err := backup.NewS3Uploader(cfg.S3Options)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			now := time.Now()
			objectKey := renderImageBedObjectKey(cfg.ImageBedOptions.Path, fileHeader.Filename, payload, now)
			contentType := detectContentType(fileHeader.Filename, payload, fileHeader.Header.Get("Content-Type"))
			s3URL, err := uploader.Upload(c.Request.Context(), objectKey, payload, contentType)
			if err != nil {
				response.InternalError(c, err)
				return
			}
			response.OK(c, gin.H{
				"url":     s3URL,
				"name":    filepath.Base(objectKey),
				"storage": "s3",
			})
			return
		}
	}

	filename := buildFileName(fileHeader.Filename)
	dir := filepath.Join(h.staticDir, typ)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		response.InternalError(c, err)
		return
	}
	savePath := filepath.Join(dir, filename)
	if err := c.SaveUploadedFile(fileHeader, savePath); err != nil {
		response.InternalError(c, err)
		return
	}

	root := detectRoot(c.Request.URL.Path)
	fileURL := h.resolveURL(c, root, typ, filename)

	if typ == "image" || typ == "photo" {
		_ = h.db.Create(&models.FileReferenceModel{
			FileURL:  fileURL,
			FileName: filename,
			Status:   "pending",
		}).Error
	}

	response.OK(c, gin.H{
		"url":     fileURL,
		"name":    filename,
		"storage": "local",
	})
}

func (h *Handler) loadConfig() (*appcfg.FullConfig, error) {
	if h.cfgSvc == nil {
		return nil, nil
	}
	return h.cfgSvc.Get()
}

func (h *Handler) delete(c *gin.Context) {
	if strings.EqualFold(c.Query("storage"), "s3") {
		response.NoContent(c)
		return
	}

	typ := normalizeType(c.Param("type"))
	name := safeName(c.Param("name"))
	if typ == "" || name == "" {
		response.BadRequest(c, "invalid path")
		return
	}

	path := filepath.Join(h.staticDir, typ, name)
	_ = os.Remove(path)

	deleteURL := strings.TrimSpace(c.Query("url"))
	if deleteURL != "" {
		_ = h.db.Where("file_url = ?", deleteURL).Delete(&models.FileReferenceModel{}).Error
	}
	_ = h.db.Where("file_name = ?", name).Delete(&models.FileReferenceModel{}).Error

	response.NoContent(c)
}

func (h *Handler) rename(c *gin.Context) {
	typ := normalizeType(c.Param("type"))
	name := safeName(c.Param("name"))
	newName := safeName(c.Query("new_name"))
	if typ == "" || name == "" || newName == "" {
		response.BadRequest(c, "invalid rename params")
		return
	}

	oldPath := filepath.Join(h.staticDir, typ, name)
	newPath := filepath.Join(h.staticDir, typ, newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) batchDeleteOrphans(c *gin.Context) {
	var dto batchOrphanDeleteDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !dto.All && len(dto.IDs) == 0 {
		response.BadRequest(c, "ids or all is required")
		return
	}

	tx := h.db.Model(&models.FileReferenceModel{}).Where("status = ?", "pending")
	if !dto.All {
		tx = tx.Where("id IN ?", dto.IDs)
	}

	var refs []models.FileReferenceModel
	if err := tx.Find(&refs).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	deleted := 0
	for _, ref := range refs {
		if path, ok := h.pathFromFileURL(ref.FileURL); ok {
			_ = os.Remove(path)
		}
		if err := h.db.Delete(&models.FileReferenceModel{}, "id = ?", ref.ID).Error; err == nil {
			deleted++
		}
	}
	response.OK(c, gin.H{"deleted": deleted})
}

func (h *Handler) batchUploadToS3(c *gin.Context) {
	var dto batchS3UploadDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	results := make([]gin.H, 0, len(dto.URLs))
	for _, u := range dto.URLs {
		results = append(results, gin.H{
			"originalUrl": u,
			"s3Url":       nil,
			"error":       "S3 sync is not supported in mx-core-go",
		})
	}
	response.OK(c, gin.H{"results": results})
}

func (h *Handler) listOrphans(c *gin.Context) {
	q := pagination.FromContext(c)
	tx := h.db.Model(&models.FileReferenceModel{}).
		Where("status = ?", "pending").
		Order("created_at DESC")

	var refs []models.FileReferenceModel
	pag, err := pagination.Paginate(tx, q, &refs)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	items := make([]gin.H, 0, len(refs))
	for _, ref := range refs {
		items = append(items, gin.H{
			"id":       ref.ID,
			"fileName": ref.FileName,
			"fileUrl":  ref.FileURL,
			"created":  ref.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       items,
		"pagination": pag,
	})
}

func (h *Handler) countOrphans(c *gin.Context) {
	var count int64
	if err := h.db.Model(&models.FileReferenceModel{}).Where("status = ?", "pending").Count(&count).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"count": count})
}

func (h *Handler) cleanupOrphans(c *gin.Context) {
	maxAgeMinutes := 60
	if raw := strings.TrimSpace(c.Query("maxAgeMinutes")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxAgeMinutes = v
		}
	}

	cutoff := time.Now().Add(-time.Duration(maxAgeMinutes) * time.Minute)
	var refs []models.FileReferenceModel
	if err := h.db.Where("status = ? AND created_at <= ?", "pending", cutoff).
		Find(&refs).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	deleted := 0
	for _, ref := range refs {
		if path, ok := h.pathFromFileURL(ref.FileURL); ok {
			_ = os.Remove(path)
		}
		if err := h.db.Delete(&models.FileReferenceModel{}, "id = ?", ref.ID).Error; err == nil {
			deleted++
		}
	}
	response.OK(c, gin.H{"deleted": deleted})
}

func (h *Handler) resolveURL(c *gin.Context, root, typ, name string) string {
	p := c.Request.URL.Path
	marker := "/" + root + "/"
	idx := strings.Index(p, marker)
	prefix := ""
	if idx >= 0 {
		prefix = p[:idx]
	}
	if prefix == "/" {
		prefix = ""
	}
	return prefix + "/" + root + "/" + typ + "/" + name
}

func (h *Handler) pathFromFileURL(raw string) (string, bool) {
	path := raw
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		path = u.Path
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i < len(parts)-2; i++ {
		seg := strings.ToLower(parts[i])
		if seg != "objects" && seg != "files" {
			continue
		}
		typ := normalizeType(parts[i+1])
		name := safeName(parts[i+2])
		if typ == "" || name == "" {
			return "", false
		}
		return filepath.Join(h.staticDir, typ, name), true
	}
	return "", false
}
