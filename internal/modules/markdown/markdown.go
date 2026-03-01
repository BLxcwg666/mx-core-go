package markdown

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	"gopkg.in/yaml.v3"
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

type articleSnapshot struct {
	ID        string
	Title     string
	Text      string
	Slug      string
	NID       int
	CreatedAt time.Time
	UpdatedAt time.Time
	Type      string
	Category  *models.CategoryModel
}

// GET /markdown/render/structure/:id
func (h *Handler) renderStructure(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.NotFound(c)
		return
	}

	article, err := h.loadArticleByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}

	html := RenderMarkdownContent(article.Text)
	structure := BuildRenderedMarkdownHTMLStructure(html, article.Title, c.Query("theme"))
	response.OK(c, structure)
}

func (h *Handler) loadArticleByID(id string) (*articleSnapshot, error) {
	var post models.PostModel
	if err := h.db.Preload("Category").Select("id, title, text, slug, category_id, created_at, updated_at").First(&post, "id = ?", id).Error; err == nil {
		return &articleSnapshot{
			ID:        post.ID,
			Title:     post.Title,
			Text:      post.Text,
			Slug:      post.Slug,
			CreatedAt: post.CreatedAt,
			UpdatedAt: post.UpdatedAt,
			Type:      "post",
			Category:  post.Category,
		}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var note models.NoteModel
	if err := h.db.Select("id, title, text, n_id, created_at, updated_at").First(&note, "id = ?", id).Error; err == nil {
		return &articleSnapshot{
			ID:        note.ID,
			Title:     note.Title,
			Text:      note.Text,
			NID:       note.NID,
			CreatedAt: note.CreatedAt,
			UpdatedAt: note.UpdatedAt,
			Type:      "note",
		}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var page models.PageModel
	if err := h.db.Select("id, title, text, slug, created_at, updated_at").First(&page, "id = ?", id).Error; err == nil {
		return &articleSnapshot{
			ID:        page.ID,
			Title:     page.Title,
			Text:      page.Text,
			Slug:      page.Slug,
			CreatedAt: page.CreatedAt,
			UpdatedAt: page.UpdatedAt,
			Type:      "page",
		}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	return nil, gorm.ErrRecordNotFound
}

// GET /markdown/export?show_title=true&slug=true&yaml=true&with_meta_json=true
func (h *Handler) export(c *gin.Context) {
	showTitle := parseBool(c.Query("show_title"))
	useSlug := parseBool(c.Query("slug"))
	includeYAML := parseBool(c.Query("yaml"))
	withMetaJSON := parseBool(c.Query("with_meta_json"))

	var posts []models.PostModel
	h.db.Preload("Category").Find(&posts)

	var notes []models.NoteModel
	h.db.Find(&notes)

	var pages []models.PageModel
	h.db.Find(&pages)

	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	postMetaMap := make(map[string]any)
	for _, p := range posts {
		meta := map[string]any{
			"created":  p.CreatedAt,
			"modified": p.UpdatedAt,
			"title":    p.Title,
			"slug":     chooseFirstNonEmpty(p.Slug, p.Title),
			"oid":      p.ID,
			"type":     "post",
			"tags":     p.Tags,
		}
		if p.Category != nil {
			meta["categories"] = p.Category.Name
			meta["permalink"] = fmt.Sprintf("/posts/%s/%s", p.Category.Slug, p.Slug)
		}
		content := markdownBuilder(meta, p.Text, includeYAML, showTitle)
		filename := markdownFilename(meta, useSlug)
		f, _ := w.Create(filepath.ToSlash(filepath.Join("posts", filename)))
		f.Write([]byte(content))
		postMetaMap[p.ID] = exportMetaWithoutText(p)
	}

	noteMetaMap := make(map[string]any)
	for _, n := range notes {
		meta := map[string]any{
			"created":   n.CreatedAt,
			"modified":  n.UpdatedAt,
			"title":     n.Title,
			"slug":      fmt.Sprintf("%d", n.NID),
			"oid":       n.ID,
			"type":      "note",
			"id":        n.NID,
			"mood":      n.Mood,
			"weather":   n.Weather,
			"permalink": fmt.Sprintf("/notes/%d", n.NID),
		}
		content := markdownBuilder(meta, n.Text, includeYAML, showTitle)
		filename := markdownFilename(meta, useSlug)
		f, _ := w.Create(filepath.ToSlash(filepath.Join("notes", filename)))
		f.Write([]byte(content))
		noteMetaMap[n.ID] = exportMetaWithoutText(n)
	}

	pageMetaMap := make(map[string]any)
	for _, pg := range pages {
		meta := map[string]any{
			"created":   pg.CreatedAt,
			"modified":  pg.UpdatedAt,
			"title":     pg.Title,
			"slug":      chooseFirstNonEmpty(pg.Slug, pg.Title),
			"oid":       pg.ID,
			"type":      "page",
			"subtitle":  pg.Subtitle,
			"permalink": "/" + pg.Slug,
		}
		content := markdownBuilder(meta, pg.Text, includeYAML, showTitle)
		filename := markdownFilename(meta, useSlug)
		f, _ := w.Create(filepath.ToSlash(filepath.Join("pages", filename)))
		f.Write([]byte(content))
		pageMetaMap[pg.ID] = exportMetaWithoutText(pg)
	}

	if withMetaJSON {
		if b, err := json.Marshal(postMetaMap); err == nil {
			f, _ := w.Create("posts/_meta.json")
			f.Write(b)
		}
		if b, err := json.Marshal(noteMetaMap); err == nil {
			f, _ := w.Create("notes/_meta.json")
			f.Write(b)
		}
		if b, err := json.Marshal(pageMetaMap); err == nil {
			f, _ := w.Create("pages/_meta.json")
			f.Write(b)
		}
	}

	w.Close()

	timestamp := time.Now().Format("20060102_150405")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="mx-export-%s.zip"`, timestamp))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}

func markdownBuilder(meta map[string]any, text string, includeYAMLHeader, showHeader bool) string {
	title := asString(meta["title"])
	var sb strings.Builder
	if includeYAMLHeader {
		header := map[string]any{
			"date":    meta["created"],
			"updated": meta["modified"],
			"title":   title,
		}
		for key, value := range meta {
			if key == "created" || key == "modified" || key == "title" {
				continue
			}
			header[key] = value
		}
		yamlText, _ := yaml.Marshal(header)
		sb.WriteString("---\n")
		sb.WriteString(strings.TrimSpace(string(yamlText)))
		sb.WriteString("\n---\n\n")
	}
	if showHeader {
		sb.WriteString("# ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
	}
	sb.WriteString(strings.TrimSpace(text))
	return sb.String()
}

// POST /markdown/import
type importDTO struct {
	Type string       `json:"type" binding:"required"`
	Data []importItem `json:"data" binding:"required"`
}

type importItem struct {
	Meta *importMeta `json:"meta"`
	Text string      `json:"text" binding:"required"`
}

type importMeta struct {
	Title      string   `json:"title"`
	Date       string   `json:"date"`
	Updated    string   `json:"updated"`
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"`
	Slug       string   `json:"slug"`
}

func (h *Handler) importMarkdown(c *gin.Context) {
	var dto importDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	docType := strings.ToLower(strings.TrimSpace(dto.Type))
	if docType != "post" && docType != "note" {
		response.BadRequest(c, "type must be post or note")
		return
	}

	if docType == "post" {
		created, err := h.importPosts(dto.Data)
		if err != nil {
			response.InternalError(c, err)
			return
		}
		response.OK(c, created)
		return
	}

	created, err := h.importNotes(dto.Data)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, created)
}

func (h *Handler) importPosts(data []importItem) ([]models.PostModel, error) {
	var categories []models.CategoryModel
	if err := h.db.Find(&categories).Error; err != nil {
		return nil, err
	}

	var defaultCategory models.CategoryModel
	if err := h.db.Order("created_at asc").First(&defaultCategory).Error; err != nil {
		return nil, errors.New("分类不存在")
	}

	categoryByName := make(map[string]models.CategoryModel, len(categories))
	categoryBySlug := make(map[string]models.CategoryModel, len(categories))
	for _, category := range categories {
		categoryByName[category.Name] = category
		categoryBySlug[category.Slug] = category
	}

	unnamedCount := 1
	createdModels := make([]models.PostModel, 0, len(data))

	for _, item := range data {
		title := fmt.Sprintf("未命名-%d", unnamedCount)
		slug := fmt.Sprintf("%d", time.Now().UnixMilli())
		categoryID := defaultCategory.ID
		tags := models.StringSlice{}
		createdAt, updatedAt := parseMetaDates(nil)

		if item.Meta != nil {
			createdAt, updatedAt = parseMetaDates(item.Meta)
			if strings.TrimSpace(item.Meta.Title) != "" {
				title = item.Meta.Title
			}
			if strings.TrimSpace(item.Meta.Slug) != "" {
				slug = item.Meta.Slug
			} else {
				slug = title
			}
			if len(item.Meta.Tags) > 0 {
				tags = append(tags, item.Meta.Tags...)
			}

			if len(item.Meta.Categories) > 0 {
				firstCategory := strings.TrimSpace(item.Meta.Categories[0])
				if firstCategory != "" {
					if existing, ok := categoryByName[firstCategory]; ok {
						categoryID = existing.ID
					} else if existing, ok := categoryBySlug[firstCategory]; ok {
						categoryID = existing.ID
					} else {
						category := models.CategoryModel{
							Name: firstCategory,
							Slug: firstCategory,
							Type: 0,
						}
						if err := h.db.Create(&category).Error; err == nil {
							categoryByName[category.Name] = category
							categoryBySlug[category.Slug] = category
							categoryID = category.ID
						}
					}
				}
			}
		} else {
			unnamedCount++
		}

		post := models.PostModel{
			WriteBase: models.WriteBase{
				Title: title,
				Text:  item.Text,
				Base: models.Base{
					CreatedAt: createdAt,
					UpdatedAt: updatedAt,
				},
			},
			Slug:       slug,
			CategoryID: &categoryID,
			Tags:       tags,
		}

		if err := h.db.Create(&post).Error; err != nil {
			continue
		}
		createdModels = append(createdModels, post)
	}
	return createdModels, nil
}

func (h *Handler) importNotes(data []importItem) ([]models.NoteModel, error) {
	var maxNID int
	if err := h.db.Model(&models.NoteModel{}).Select("COALESCE(MAX(n_id), 0)").Scan(&maxNID).Error; err != nil {
		return nil, err
	}

	createdModels := make([]models.NoteModel, 0, len(data))
	for _, item := range data {
		title := "未命名记录"
		createdAt, updatedAt := parseMetaDates(nil)
		if item.Meta != nil {
			createdAt, updatedAt = parseMetaDates(item.Meta)
			if strings.TrimSpace(item.Meta.Title) != "" {
				title = item.Meta.Title
			}
		}

		maxNID++
		note := models.NoteModel{
			WriteBase: models.WriteBase{
				Title: title,
				Text:  item.Text,
				Base: models.Base{
					CreatedAt: createdAt,
					UpdatedAt: updatedAt,
				},
			},
			NID: maxNID,
		}

		if err := h.db.Create(&note).Error; err != nil {
			maxNID--
			continue
		}
		createdModels = append(createdModels, note)
	}
	return createdModels, nil
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func chooseFirstNonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func markdownFilename(meta map[string]any, useSlug bool) string {
	filename := asString(meta["title"])
	if useSlug {
		filename = asString(meta["slug"])
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "untitled"
	}
	filename = strings.ReplaceAll(filename, "/", "-")
	filename = strings.ReplaceAll(filename, "\\", "-")
	return filename + ".md"
}

func asString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func parseMetaDates(meta *importMeta) (time.Time, time.Time) {
	now := time.Now()
	if meta == nil {
		return now, now
	}

	created := parseTime(meta.Date)
	if created.IsZero() {
		created = now
	}

	updated := parseTime(meta.Updated)
	if updated.IsZero() {
		updated = created
	}
	return created, updated
}

func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func exportMetaWithoutText(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}

	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	delete(out, "text")
	delete(out, "__v")
	return out
}
