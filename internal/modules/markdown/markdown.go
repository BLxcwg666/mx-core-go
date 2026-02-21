package markdown

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

// Handler handles markdown import/export endpoints.
type Handler struct{ db *gorm.DB }

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	rg.GET("/markdown/render/structure/:id", h.renderStructure)

	g := rg.Group("/markdown", authMW)
	g.GET("/export", h.export)
	g.POST("/import", h.importMarkdown)
}

// GET /markdown/render/structure/:id — extract heading structure from an article
func (h *Handler) renderStructure(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.NotFound(c)
		return
	}

	title, text, err := h.loadArticleByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}

	response.OK(c, gin.H{
		"title":    title,
		"headings": extractHeadings(text),
	})
}

type headingItem struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

// extractHeadings parses ATX-style Markdown headings from text.
func extractHeadings(text string) []headingItem {
	var headings []headingItem
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		level := 0
		for _, r := range line {
			if r == '#' {
				level++
			} else {
				break
			}
		}
		if level < 1 || level > 6 {
			continue
		}
		content := strings.TrimSpace(line[level:])
		if content == "" {
			continue
		}
		headings = append(headings, headingItem{
			Level: level,
			Text:  content,
			ID:    slugifyHeading(content),
		})
	}
	if headings == nil {
		headings = []headingItem{}
	}
	return headings
}

// slugifyHeading converts heading text to a URL-friendly anchor ID.
func slugifyHeading(text string) string {
	s := strings.ToLower(text)
	s = strings.ReplaceAll(s, " ", "-")
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	result := strings.Trim(sb.String(), "-")
	if result == "" {
		result = "heading"
	}
	return result
}

// loadArticleByID searches posts, notes, pages by ID.
func (h *Handler) loadArticleByID(id string) (title, text string, err error) {
	var post models.PostModel
	if err = h.db.Select("id, title, text").First(&post, "id = ?", id).Error; err == nil {
		return post.Title, post.Text, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", err
	}

	var note models.NoteModel
	if err = h.db.Select("id, title, text").First(&note, "id = ?", id).Error; err == nil {
		return note.Title, note.Text, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", err
	}

	var page models.PageModel
	if err = h.db.Select("id, title, text").First(&page, "id = ?", id).Error; err == nil {
		return page.Title, page.Text, nil
	}
	return "", "", err
}

// GET /markdown/export?show_title=true&slug=true&yaml=true
func (h *Handler) export(c *gin.Context) {
	showTitle := c.Query("show_title") == "true" || c.Query("show_title") == "1"
	useSlug := c.Query("slug") == "true" || c.Query("slug") == "1"
	includeYAML := c.Query("yaml") == "true" || c.Query("yaml") == "1"

	var posts []models.PostModel
	h.db.Find(&posts)

	var notes []models.NoteModel
	h.db.Find(&notes)

	var pages []models.PageModel
	h.db.Find(&pages)

	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	for _, p := range posts {
		filename := p.ID
		if useSlug && p.Slug != "" {
			filename = p.Slug
		}
		content := buildMarkdown(p.Title, p.Text, showTitle, includeYAML, map[string]string{
			"slug": p.Slug,
			"date": p.CreatedAt.Format(time.RFC3339),
		})
		f, _ := w.Create(fmt.Sprintf("posts/%s.md", filename))
		f.Write([]byte(content))
	}

	for _, n := range notes {
		filename := fmt.Sprintf("%d", n.NID)
		content := buildMarkdown(n.Title, n.Text, showTitle, includeYAML, map[string]string{
			"nid":  fmt.Sprintf("%d", n.NID),
			"date": n.CreatedAt.Format(time.RFC3339),
		})
		f, _ := w.Create(fmt.Sprintf("notes/%s.md", filename))
		f.Write([]byte(content))
	}

	for _, pg := range pages {
		filename := pg.ID
		if useSlug && pg.Slug != "" {
			filename = pg.Slug
		}
		content := buildMarkdown(pg.Title, pg.Text, showTitle, includeYAML, map[string]string{
			"slug": pg.Slug,
			"date": pg.CreatedAt.Format(time.RFC3339),
		})
		f, _ := w.Create(fmt.Sprintf("pages/%s.md", filename))
		f.Write([]byte(content))
	}

	w.Close()

	timestamp := time.Now().Format("20060102_150405")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="mx-export-%s.zip"`, timestamp))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}

func buildMarkdown(title, text string, showTitle, includeYAML bool, meta map[string]string) string {
	var sb strings.Builder
	if includeYAML {
		sb.WriteString("---\n")
		sb.WriteString(fmt.Sprintf("title: %q\n", title))
		for k, v := range meta {
			sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
		}
		sb.WriteString("---\n\n")
	}
	if showTitle {
		sb.WriteString(fmt.Sprintf("# %s\n\n", title))
	}
	sb.WriteString(text)
	return sb.String()
}

// POST /markdown/import
type importDTO struct {
	Type string       `json:"type" binding:"required"`
	Data []importItem `json:"data" binding:"required"`
}

type importItem struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Slug  string `json:"slug"`
	Date  string `json:"date"`
}

func (h *Handler) importMarkdown(c *gin.Context) {
	var dto importDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	imported := 0
	for _, item := range dto.Data {
		switch dto.Type {
		case "post":
			slug := item.Slug
			if slug == "" {
				slug = sanitizeSlug(item.Title)
			}

			var count int64
			h.db.Model(&models.PostModel{}).Where("slug = ?", slug).Count(&count)
			if count > 0 {
				continue
			}
			p := models.PostModel{
				WriteBase: models.WriteBase{
					Base:  models.Base{},
					Title: item.Title,
					Text:  item.Text,
				},
				Slug:        slug,
				IsPublished: true,
			}
			if err := h.db.Create(&p).Error; err == nil {
				imported++
			}
		case "note":
			var maxNID int
			h.db.Model(&models.NoteModel{}).Select("COALESCE(MAX(n_id), 0)").Scan(&maxNID)
			n := models.NoteModel{
				WriteBase: models.WriteBase{
					Title: item.Title,
					Text:  item.Text,
				},
				NID:         maxNID + 1,
				IsPublished: true,
			}
			if err := h.db.Create(&n).Error; err == nil {
				imported++
			}
		}
	}

	response.OK(c, gin.H{"imported": imported})
}

func sanitizeSlug(title string) string {
	s := strings.ToLower(title)
	s = strings.ReplaceAll(s, " ", "-")

	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	result := sb.String()
	if result == "" {
		result = fmt.Sprintf("import-%d", time.Now().UnixMilli())
	}
	return result
}
