package init_

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/modules/backup"
	appconfigs "github.com/mx-space/core/internal/modules/configs"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

// Handler handles setup wizard endpoints.
type Handler struct {
	db     *gorm.DB
	cfgSvc *appconfigs.Service
}

func NewHandler(db *gorm.DB, cfgSvc *appconfigs.Service) *Handler {
	return &Handler{db: db, cfgSvc: cfgSvc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/init")

	g.GET("", h.checkInit)
	g.GET("/configs/default", h.defaultConfigs)
	g.PATCH("/configs/:key", h.patchConfigKey)
	g.POST("/restore", h.restore)
}

// isInitialized returns true if at least one user exists in the database.
func isInitialized(db *gorm.DB) bool {
	var count int64
	db.Table("users").Count(&count)
	return count > 0
}

// GET /init — {isInit: bool}
func (h *Handler) checkInit(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"isInit": isInitialized(h.db)})
}

// GET /init/configs/default — returns default configs
func (h *Handler) defaultConfigs(c *gin.Context) {
	if isInitialized(h.db) {
		response.ForbiddenMsg(c, "system is already initialized")
		return
	}
	defaults := config.DefaultFullConfig()
	response.OK(c, defaults)
}

// PATCH /init/configs/:key — update config section unauthenticated
func (h *Handler) patchConfigKey(c *gin.Context) {
	if isInitialized(h.db) {
		response.ForbiddenMsg(c, "system is already initialized")
		return
	}
	key := c.Param("key")
	var body json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.cfgSvc.Patch(map[string]json.RawMessage{key: body})
	if err != nil {
		response.InternalError(c, err)
		return
	}

	full, _ := json.Marshal(updated)
	var m map[string]json.RawMessage
	json.Unmarshal(full, &m)
	if val, ok := m[key]; ok {
		var result interface{}
		json.Unmarshal(val, &result)
		response.OK(c, result)
		return
	}
	response.OK(c, updated)
}

// POST /init/restore — upload and restore from backup ZIP
func (h *Handler) restore(c *gin.Context) {
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

	if err := backup.RestoreFromZip(h.db, zr); err != nil {
		response.InternalError(c, err)
		return
	}
	if h.cfgSvc != nil {
		h.cfgSvc.Invalidate()
	}

	response.OK(c, gin.H{"message": "restore successful"})
}
