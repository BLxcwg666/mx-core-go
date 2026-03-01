package helper

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db     *gorm.DB
	cfgSvc *configs.Service
}

func NewHandler(db *gorm.DB, cfgSvc *configs.Service) *Handler {
	return &Handler{db: db, cfgSvc: cfgSvc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/helper")
	g.GET("/url-builder/:id", h.urlBuilderByID)

	a := g.Group("", authMW)
	a.POST("/refresh-images", h.refreshImages)
}

func (h *Handler) urlBuilderByID(c *gin.Context) {
	docID := c.Param("id")
	url, canRedirect, err := h.buildURLByID(docID)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	redirect := c.Query("redirect")
	shouldRedirect := redirect == "1" || strings.EqualFold(redirect, "true")
	if shouldRedirect {
		if !canRedirect || url == "" {
			response.NotFoundMsg(c, "内容不存在或该类型无法跳转")
			return
		}
		c.Redirect(301, url)
		return
	}

	if url == "" {
		response.OK(c, nil)
		return
	}
	response.OK(c, gin.H{"data": url})
}

func (h *Handler) refreshImages(c *gin.Context) {
	refreshPostImages(h.db)
	refreshNoteImages(h.db)
	refreshPageImages(h.db)
	response.NoContent(c)
}

var (
	markdownImagePattern = regexp.MustCompile(`!\[[^\]]*]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	htmlImagePattern     = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["'][^>]*>`)
)

func refreshPostImages(db *gorm.DB) {
	var rows []models.PostModel
	if err := db.Select("id", "text", "images").Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		next := rebuildImages(row.Text, row.Images)
		_ = db.Model(&models.PostModel{}).Where("id = ?", row.ID).Update("images", next).Error
	}
}

func refreshNoteImages(db *gorm.DB) {
	var rows []models.NoteModel
	if err := db.Select("id", "text", "images").Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		next := rebuildImages(row.Text, row.Images)
		_ = db.Model(&models.NoteModel{}).Where("id = ?", row.ID).Update("images", next).Error
	}
}

func refreshPageImages(db *gorm.DB) {
	var rows []models.PageModel
	if err := db.Select("id", "text", "images").Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		next := rebuildImages(row.Text, row.Images)
		_ = db.Model(&models.PageModel{}).Where("id = ?", row.ID).Update("images", next).Error
	}
}

func rebuildImages(markdown string, origin []models.Image) []models.Image {
	srcs := pickImagesFromMarkdown(markdown)
	if len(srcs) == 0 && len(origin) == 0 {
		return []models.Image{}
	}

	oldBySrc := make(map[string]models.Image, len(origin))
	for _, item := range origin {
		if src := strings.TrimSpace(item.Src); src != "" {
			oldBySrc[src] = item
		}
	}

	next := make([]models.Image, 0, len(srcs))
	newSet := make(map[string]struct{}, len(srcs))
	for _, src := range srcs {
		newSet[src] = struct{}{}
		if old, ok := oldBySrc[src]; ok {
			next = append(next, old)
			continue
		}
		name := filepath.Base(strings.SplitN(src, "?", 2)[0])
		next = append(next, models.Image{
			Name: name,
			Src:  src,
		})
	}

	if len(origin) > 0 {
		front := make([]models.Image, 0, len(origin))
		for _, old := range origin {
			src := strings.TrimSpace(old.Src)
			if src == "" {
				continue
			}
			if _, ok := newSet[src]; !ok {
				front = append(front, old)
			}
		}
		next = append(front, next...)
	}

	return next
}

func pickImagesFromMarkdown(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{}
	}

	seen := make(map[string]struct{})
	out := make([]string, 0)
	appendSrc := func(src string) {
		src = strings.TrimSpace(src)
		if src == "" {
			return
		}
		if isVideoByExt(src) {
			return
		}
		if _, ok := seen[src]; ok {
			return
		}
		seen[src] = struct{}{}
		out = append(out, src)
	}

	for _, m := range markdownImagePattern.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			appendSrc(m[1])
		}
	}
	for _, m := range htmlImagePattern.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			appendSrc(m[1])
		}
	}

	return out
}

func isVideoByExt(src string) bool {
	lower := strings.ToLower(strings.SplitN(src, "?", 2)[0])
	for _, ext := range []string{".mp4", ".webm", ".ogg", ".mov", ".avi", ".flv", ".mkv"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func (h *Handler) buildURLByID(id string) (string, bool, error) {
	base, err := h.getWebBase()
	if err != nil {
		return "", false, err
	}

	var post models.PostModel
	if err := h.db.Preload("Category").First(&post, "id = ?", id).Error; err == nil {
		categorySlug := "uncategorized"
		if post.Category.Slug != "" {
			categorySlug = post.Category.Slug
		}
		return base + "/posts/" + categorySlug + "/" + post.Slug, true, nil
	}

	var note models.NoteModel
	if err := h.db.First(&note, "id = ?", id).Error; err == nil {
		return base + "/notes/" + itoa(note.NID), true, nil
	}

	var page models.PageModel
	if err := h.db.First(&page, "id = ?", id).Error; err == nil {
		return base + "/" + strings.TrimPrefix(page.Slug, "/"), true, nil
	}

	var recently models.RecentlyModel
	if err := h.db.First(&recently, "id = ?", id).Error; err == nil {
		// Keep behavior consistent with upstream helper: recently cannot redirect.
		return "", false, nil
	}

	return "", false, nil
}

func (h *Handler) getWebBase() (string, error) {
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(strings.TrimSpace(cfg.URL.WebURL), "/")
	return base, nil
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
