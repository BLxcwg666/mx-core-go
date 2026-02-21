package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/configs"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// SearchResult is a single search hit returned to the client.
type SearchResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Type    string `json:"type"` // post | note | page
	Slug    string `json:"slug,omitempty"`
	NID     int    `json:"nid,omitempty"`
}

// Service handles search indexing and querying.
type Service struct {
	db      *gorm.DB
	cfgSvc  *configs.Service
	runtime *appcfg.AppConfig
	meili   *meiliClient
}

func NewService(db *gorm.DB, cfgSvc *configs.Service, runtime *appcfg.AppConfig) *Service {
	return &Service{db: db, cfgSvc: cfgSvc, runtime: runtime}
}

func (s *Service) ensureClient() (*meiliClient, error) {
	cfg, err := s.cfgSvc.Get()
	if err != nil {
		return nil, err
	}

	enable := cfg.MeiliSearchOptions.Enable
	host := strings.TrimSpace(cfg.MeiliSearchOptions.Host)
	apiKey := strings.TrimSpace(cfg.MeiliSearchOptions.APIKey)
	indexName := strings.TrimSpace(cfg.MeiliSearchOptions.IndexName)

	if s.runtime != nil {
		if s.runtime.MeiliSearch.HasEnable {
			enable = s.runtime.MeiliSearch.Enable
		}
		if host == "" {
			host = s.runtime.MeiliSearch.Endpoint()
		}
		if apiKey == "" {
			apiKey = s.runtime.MeiliSearch.APIKey
		}
		if indexName == "" {
			indexName = s.runtime.MeiliSearch.IndexName
		}
	}
	if !enable {
		return nil, fmt.Errorf("MeiliSearch is disabled")
	}
	if s.meili == nil || s.meili.host != host || s.meili.apiKey != apiKey || s.meili.indexName != indexName {
		s.meili = newMeiliClient(host, apiKey, indexName)
	}
	return s.meili, nil
}

// Search queries MeiliSearch, with MySQL LIKE fallback.
func (s *Service) Search(q string) ([]SearchResult, error) {
	if client, err := s.ensureClient(); err == nil {
		if results, err := client.Search(q); err == nil {
			return results, nil
		}
	}
	return s.mysqlSearch(q)
}

func (s *Service) SearchByType(docType, keyword string, page, size int) ([]SearchResult, response.Pagination, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}

	like := "%" + keyword + "%"
	offset := (page - 1) * size
	results := make([]SearchResult, 0, size)
	var total int64

	switch docType {
	case "post":
		tx := s.db.Model(&models.PostModel{})
		if keyword != "" {
			tx = tx.Where("title LIKE ? OR text LIKE ?", like, like)
		}
		if err := tx.Count(&total).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		var posts []models.PostModel
		if err := tx.
			Select("id, slug, title, summary").
			Order("created_at DESC").
			Offset(offset).
			Limit(size).
			Find(&posts).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		for _, p := range posts {
			results = append(results, SearchResult{
				ID: p.ID, Title: p.Title, Summary: p.Summary, Type: "post", Slug: p.Slug,
			})
		}

	case "note":
		tx := s.db.Model(&models.NoteModel{})
		if keyword != "" {
			tx = tx.Where("title LIKE ? OR text LIKE ?", like, like)
		}
		if err := tx.Count(&total).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		var notes []models.NoteModel
		if err := tx.
			Select("id, n_id, title").
			Order("created_at DESC").
			Offset(offset).
			Limit(size).
			Find(&notes).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		for _, n := range notes {
			results = append(results, SearchResult{
				ID: n.ID, Title: n.Title, Type: "note", NID: n.NID,
			})
		}

	case "page":
		tx := s.db.Model(&models.PageModel{})
		if keyword != "" {
			tx = tx.Where("title LIKE ? OR text LIKE ?", like, like)
		}
		if err := tx.Count(&total).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		var pages []models.PageModel
		if err := tx.
			Select("id, slug, title").
			Order("created_at DESC").
			Offset(offset).
			Limit(size).
			Find(&pages).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		for _, pg := range pages {
			results = append(results, SearchResult{
				ID: pg.ID, Title: pg.Title, Type: "page", Slug: pg.Slug,
			})
		}

	default:
		return []SearchResult{}, response.Pagination{
			Total:       0,
			CurrentPage: page,
			TotalPage:   0,
			Size:        size,
			HasNextPage: false,
		}, nil
	}

	totalPage := int((total + int64(size) - 1) / int64(size))
	pag := response.Pagination{
		Total:       total,
		CurrentPage: page,
		TotalPage:   totalPage,
		Size:        size,
		HasNextPage: page < totalPage,
	}
	return results, pag, nil
}

func (s *Service) mysqlSearch(q string) ([]SearchResult, error) {
	like := "%" + q + "%"
	var results []SearchResult

	var posts []models.PostModel
	s.db.Where("is_published = ? AND (title LIKE ? OR text LIKE ?)", true, like, like).
		Select("id, slug, title, summary").Limit(10).Find(&posts)
	for _, p := range posts {
		results = append(results, SearchResult{
			ID: p.ID, Title: p.Title, Summary: p.Summary, Type: "post", Slug: p.Slug,
		})
	}

	var notes []models.NoteModel
	s.db.Where("is_published = ? AND (title LIKE ? OR text LIKE ?)", true, like, like).
		Select("id, n_id, title").Limit(10).Find(&notes)
	for _, n := range notes {
		results = append(results, SearchResult{
			ID: n.ID, Title: n.Title, Type: "note", NID: n.NID,
		})
	}

	var pages []models.PageModel
	s.db.Where("title LIKE ? OR text LIKE ?", like, like).
		Select("id, slug, title").Limit(5).Find(&pages)
	for _, pg := range pages {
		results = append(results, SearchResult{
			ID: pg.ID, Title: pg.Title, Type: "page", Slug: pg.Slug,
		})
	}

	return results, nil
}

// IndexAll rebuilds the full MeiliSearch index from the database.
func (s *Service) IndexAll() error {
	client, err := s.ensureClient()
	if err != nil {
		return err
	}
	var docs []map[string]interface{}

	var posts []models.PostModel
	s.db.Where("is_published = ?", true).Find(&posts)
	for _, p := range posts {
		docs = append(docs, map[string]interface{}{
			"id": p.ID, "title": p.Title, "text": p.Text,
			"summary": p.Summary, "type": "post", "slug": p.Slug,
		})
	}

	var notes []models.NoteModel
	s.db.Where("is_published = ?", true).Find(&notes)
	for _, n := range notes {
		docs = append(docs, map[string]interface{}{
			"id": n.ID, "title": n.Title, "text": n.Text,
			"type": "note", "nid": n.NID,
		})
	}

	var pages []models.PageModel
	s.db.Find(&pages)
	for _, pg := range pages {
		docs = append(docs, map[string]interface{}{
			"id": pg.ID, "title": pg.Title, "text": pg.Text,
			"type": "page", "slug": pg.Slug,
		})
	}

	return client.AddDocuments(docs)
}

// IndexDocument upserts one document into MeiliSearch (call after create/update).
func (s *Service) IndexDocument(id, title, text, docType, slug string, nid int) {
	client, err := s.ensureClient()
	if err != nil {
		return
	}
	doc := map[string]interface{}{"id": id, "title": title, "text": text, "type": docType}
	if slug != "" {
		doc["slug"] = slug
	}
	if nid > 0 {
		doc["nid"] = nid
	}
	_ = client.AddDocuments([]map[string]interface{}{doc})
}

// GetAllDocuments returns all published documents (posts+notes+pages) as SearchResults.
func (s *Service) GetAllDocuments() ([]SearchResult, error) {
	var results []SearchResult

	var posts []models.PostModel
	if err := s.db.Where("is_published = ?", true).Select("id, slug, title, summary").Find(&posts).Error; err != nil {
		return nil, err
	}
	for _, p := range posts {
		results = append(results, SearchResult{
			ID: p.ID, Title: p.Title, Summary: p.Summary, Type: "post", Slug: p.Slug,
		})
	}

	var notes []models.NoteModel
	if err := s.db.Where("is_published = ?", true).Select("id, n_id, title").Find(&notes).Error; err != nil {
		return nil, err
	}
	for _, n := range notes {
		results = append(results, SearchResult{
			ID: n.ID, Title: n.Title, Type: "note", NID: n.NID,
		})
	}

	var pages []models.PageModel
	if err := s.db.Select("id, slug, title").Find(&pages).Error; err != nil {
		return nil, err
	}
	for _, pg := range pages {
		results = append(results, SearchResult{
			ID: pg.ID, Title: pg.Title, Type: "page", Slug: pg.Slug,
		})
	}

	if results == nil {
		results = []SearchResult{}
	}
	return results, nil
}

// DeleteDocument removes a document from the index (call after delete).
func (s *Service) DeleteDocument(id string) {
	if client, err := s.ensureClient(); err == nil {
		_ = client.DeleteDocument(id)
	}
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/search")
	g.GET("", h.search)
	g.GET("/type/:type", h.searchByType)
	g.POST("/index", authMW, h.reindex)
	g.POST("/meili/push", authMW, h.reindex)

	// Algolia-compatible stubs (reuse MeiliSearch implementation)
	g.GET("/algolia", h.search)
	g.POST("/algolia/push", authMW, h.reindex)
	g.GET("/algolia/import-json", authMW, h.algoliaExportJSON)
}

func (h *Handler) search(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		response.BadRequest(c, "q is required")
		return
	}
	results, err := h.svc.Search(q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if results == nil {
		results = []SearchResult{}
	}
	response.OK(c, gin.H{"data": results, "query": q})
}

func (h *Handler) reindex(c *gin.Context) {
	go h.svc.IndexAll()
	response.OK(c, gin.H{"message": "indexing started"})
}

func (h *Handler) algoliaExportJSON(c *gin.Context) {
	docs, err := h.svc.GetAllDocuments()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	data, err := json.Marshal(docs)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	filename := fmt.Sprintf("algolia-export-%s.json", time.Now().Format("20060102_150405"))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(200, "application/json", data)
}

func (h *Handler) searchByType(c *gin.Context) {
	docType := c.Param("type")
	keyword := c.Query("keyword")
	if keyword == "" {
		keyword = c.Query("q")
	}

	page := 1
	size := 20
	if v := c.Query("page"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			page = i
		}
	}
	if v := c.Query("size"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			size = i
		}
	}

	results, pag, err := h.svc.SearchByType(docType, keyword, page, size)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, results, pag)
}

// MeiliSearch HTTP client

type meiliClient struct {
	host      string
	apiKey    string
	indexName string
}

func newMeiliClient(host, apiKey, indexName string) *meiliClient {
	if host == "" {
		host = "http://localhost:7700"
	}
	if indexName == "" {
		indexName = "mx-space"
	}
	return &meiliClient{host: host, apiKey: apiKey, indexName: indexName}
}

func (m *meiliClient) Search(q string) ([]SearchResult, error) {
	body, _ := json.Marshal(map[string]interface{}{"q": q, "limit": 20})
	data, err := m.do("POST", fmt.Sprintf("/indexes/%s/search", m.indexName), body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Hits []map[string]interface{} `json:"hits"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	var results []SearchResult
	for _, hit := range resp.Hits {
		r := SearchResult{}
		if v, _ := hit["id"].(string); v != "" {
			r.ID = v
		}
		if v, _ := hit["title"].(string); v != "" {
			r.Title = v
		}
		if v, _ := hit["summary"].(string); v != "" {
			r.Summary = v
		}
		if v, _ := hit["type"].(string); v != "" {
			r.Type = v
		}
		if v, _ := hit["slug"].(string); v != "" {
			r.Slug = v
		}
		if v, _ := hit["nid"].(float64); v > 0 {
			r.NID = int(v)
		}
		results = append(results, r)
	}
	return results, nil
}

func (m *meiliClient) AddDocuments(docs []map[string]interface{}) error {
	body, _ := json.Marshal(docs)
	_, err := m.do("POST", fmt.Sprintf("/indexes/%s/documents", m.indexName), body)
	return err
}

func (m *meiliClient) DeleteDocument(id string) error {
	_, err := m.do("DELETE", fmt.Sprintf("/indexes/%s/documents/%s", m.indexName, id), nil)
	return err
}

func (m *meiliClient) do(method, path string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, m.host+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("meili error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}
