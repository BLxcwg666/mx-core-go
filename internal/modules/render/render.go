package render

import (
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/render")
	g.GET("/markdown/:id", h.renderArticle)
	g.POST("/markdown", authMW, h.previewMarkdown)
}

func (h *Handler) renderArticle(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.NotFound(c)
		return
	}

	title, text, isPrivate, err := h.loadRenderableByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}

	if isPrivate && !hasRenderAccess(c) {
		response.Forbidden(c)
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, renderHTML(title, text))
}

type markdownPreviewDTO struct {
	MD    string `json:"md" binding:"required"`
	Title string `json:"title"`
}

func (h *Handler) previewMarkdown(c *gin.Context) {
	var dto markdownPreviewDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, renderHTML(dto.Title, dto.MD))
}

func (h *Handler) loadRenderableByID(id string) (title, text string, isPrivate bool, err error) {
	var post models.PostModel
	if err = h.db.Select("id, title, text, is_published").First(&post, "id = ?", id).Error; err == nil {
		return post.Title, post.Text, !post.IsPublished, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", false, err
	}

	var note models.NoteModel
	if err = h.db.Select("id, title, text, is_published, password_hash").First(&note, "id = ?", id).Error; err == nil {
		private := !note.IsPublished || strings.TrimSpace(note.Password) != ""
		return note.Title, note.Text, private, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", false, err
	}

	var page models.PageModel
	if err = h.db.Select("id, title, text").First(&page, "id = ?", id).Error; err == nil {
		return page.Title, page.Text, false, nil
	}
	return "", "", false, err
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

func renderHTML(title, markdown string) string {
	escapedTitle := template.HTMLEscapeString(strings.TrimSpace(title))
	escapedContent := template.HTMLEscapeString(markdown)
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>` + escapedTitle + `</title>
  <style>
    body { margin: 0; padding: 24px; font: 16px/1.7 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #222; background: #fff; }
    main { max-width: 860px; margin: 0 auto; }
    h1 { margin: 0 0 20px; font-size: 28px; }
    pre { white-space: pre-wrap; word-break: break-word; border: 1px solid #eee; border-radius: 8px; padding: 16px; background: #fafafa; }
  </style>
</head>
<body>
  <main>
    <h1>` + escapedTitle + `</h1>
    <pre>` + escapedContent + `</pre>
  </main>
</body>
</html>`
}
