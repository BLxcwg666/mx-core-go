package render

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/configs"
	mdmodule "github.com/mx-space/core/internal/modules/markdown"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db     *gorm.DB
	cfgSvc *appconfigs.Service
}

func NewHandler(db *gorm.DB, cfgSvc *appconfigs.Service) *Handler {
	return &Handler{db: db, cfgSvc: cfgSvc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/render")
	g.GET("/markdown/:id", h.renderArticle)
	g.POST("/markdown", authMW, h.previewMarkdown)
}

func (h *Handler) renderArticle(c *gin.Context) {
	start := time.Now()
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.NotFound(c)
		return
	}

	doc, err := h.loadRenderableByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}

	if doc.IsPrivate && !hasRenderAccess(c) {
		response.Forbidden(c)
		return
	}

	html := mdmodule.RenderMarkdownContent(doc.Text)
	structure := mdmodule.BuildRenderedMarkdownHTMLStructure(html, doc.Title, c.Query("theme"))

	sourceURL := h.resolveSourceURL(c, doc.RelativePath())
	username := h.resolveMasterName()
	renderedAt := time.Now().Format("2006-01-02 15:04:05")
	createdAt := doc.CreatedAt.Format("2006-01-02 15:04")
	renderCostMs := float64(time.Since(start).Microseconds()) / 1000.0

	footer := fmt.Sprintf(`<div>本文渲染于 %s，由 marked.js 解析生成，用时 %.2fms</div>
      <div>作者：%s，撰写于%s</div>
      <div>原文地址：<a href="%s">%s</a></div>`,
		template.HTMLEscapeString(renderedAt),
		renderCostMs,
		template.HTMLEscapeString(username),
		template.HTMLEscapeString(createdAt),
		template.HTMLEscapeString(sourceURL),
		template.HTMLEscapeString(sourceURL),
	)

	info := ""
	if doc.IsPrivate {
		info = "正在查看的文章还未公开"
	}

	page := mdmodule.RenderMarkdownHTMLDocument(structure, mdmodule.RenderDocumentOptions{
		Title:  doc.Title,
		Info:   template.HTMLEscapeString(info),
		Footer: footer,
	})

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, page)
}

type markdownPreviewDTO struct {
	MD    string `json:"md" binding:"required"`
	Title string `json:"title" binding:"required"`
}

func (h *Handler) previewMarkdown(c *gin.Context) {
	var dto markdownPreviewDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	html := mdmodule.RenderMarkdownContent(dto.MD)
	structure := mdmodule.BuildRenderedMarkdownHTMLStructure(html, dto.Title, c.Query("theme"))
	page := mdmodule.RenderMarkdownHTMLDocument(structure, mdmodule.RenderDocumentOptions{
		Title: dto.Title,
	})

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, page)
}

type renderableDocument struct {
	Type         string
	Title        string
	Text         string
	Slug         string
	NID          int
	CategorySlug string
	CreatedAt    time.Time
	IsPrivate    bool
}

func (d renderableDocument) RelativePath() string {
	switch d.Type {
	case "posts":
		return fmt.Sprintf("/posts/%s/%s", d.CategorySlug, d.Slug)
	case "notes":
		return fmt.Sprintf("/notes/%d", d.NID)
	case "pages":
		return "/" + strings.TrimPrefix(d.Slug, "/")
	default:
		return "/"
	}
}

func (h *Handler) loadRenderableByID(id string) (*renderableDocument, error) {
	var post models.PostModel
	if err := h.db.Preload("Category").
		Select("id, title, text, slug, category_id, is_published, created_at").
		First(&post, "id = ?", id).Error; err == nil {
		categorySlug := ""
		if post.Category != nil {
			categorySlug = post.Category.Slug
		}
		return &renderableDocument{
			Type:         "posts",
			Title:        post.Title,
			Text:         post.Text,
			Slug:         post.Slug,
			CategorySlug: categorySlug,
			CreatedAt:    post.CreatedAt,
			IsPrivate:    !post.IsPublished,
		}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var note models.NoteModel
	if err := h.db.Select("id, title, text, n_id, is_published, password_hash, created_at").First(&note, "id = ?", id).Error; err == nil {
		return &renderableDocument{
			Type:      "notes",
			Title:     note.Title,
			Text:      note.Text,
			NID:       note.NID,
			CreatedAt: note.CreatedAt,
			IsPrivate: !note.IsPublished || strings.TrimSpace(note.Password) != "",
		}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var page models.PageModel
	if err := h.db.Select("id, title, text, slug, created_at").First(&page, "id = ?", id).Error; err == nil {
		return &renderableDocument{
			Type:      "pages",
			Title:     page.Title,
			Text:      page.Text,
			Slug:      page.Slug,
			CreatedAt: page.CreatedAt,
			IsPrivate: false,
		}, nil
	}

	return nil, gorm.ErrRecordNotFound
}

func hasRenderAccess(c *gin.Context) bool {
	if middleware.IsAuthenticated(c) {
		return true
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	_, err := jwtpkg.Parse(token)
	return err == nil
}

func (h *Handler) resolveSourceURL(c *gin.Context, relativePath string) string {
	webURL := h.resolveWebURL(c)
	base, err := url.Parse(webURL)
	if err != nil {
		return relativePath
	}
	ref, err := url.Parse(relativePath)
	if err != nil {
		return relativePath
	}
	return base.ResolveReference(ref).String()
}

func (h *Handler) resolveWebURL(c *gin.Context) string {
	if h.cfgSvc != nil {
		if cfg, err := h.cfgSvc.Get(); err == nil {
			if webURL := strings.TrimSpace(cfg.URL.WebURL); webURL != "" {
				if !strings.HasPrefix(webURL, "http://") && !strings.HasPrefix(webURL, "https://") {
					webURL = "http://" + webURL
				}
				return webURL
			}
		}
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := strings.TrimSpace(c.Request.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.Split(proto, ",")[0]
	}

	host := strings.TrimSpace(c.Request.Host)
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func (h *Handler) resolveMasterName() string {
	var user models.UserModel
	if err := h.db.Select("name, username").Order("created_at asc").First(&user).Error; err != nil {
		return "owner"
	}
	if strings.TrimSpace(user.Name) != "" {
		return user.Name
	}
	if strings.TrimSpace(user.Username) != "" {
		return user.Username
	}
	return "owner"
}
