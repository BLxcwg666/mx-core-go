package search

import (
	"fmt"
	"strings"

	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/response"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service handles search indexing and querying.
type Service struct {
	db      *gorm.DB
	cfgSvc  *configs.Service
	runtime *appcfg.AppConfig
	meili   *meiliClient
	logger  *zap.Logger
}

func NewService(db *gorm.DB, cfgSvc *configs.Service, runtime *appcfg.AppConfig, opts ...ServiceOption) *Service {
	s := &Service{db: db, cfgSvc: cfgSvc, runtime: runtime, logger: zap.NewNop()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ServiceOption configures a search Service.
type ServiceOption func(*Service)

// WithLogger sets the logger for the search service.
func WithLogger(l *zap.Logger) ServiceOption {
	return func(s *Service) {
		if l != nil {
			s.logger = l.Named("SearchService")
		}
	}
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
func (s *Service) Search(q string) ([]SearchResult, string, error) {
	if client, err := s.ensureClient(); err == nil {
		if results, err := client.Search(q); err == nil {
			s.logger.Debug(fmt.Sprintf("MeiliSearch 搜索命中 %d 条结果", len(results)))
			return results, servedByMeili, nil
		} else {
			s.logger.Debug("MeiliSearch 搜索失败，回退到 MySQL", zap.Error(err))
		}
	}
	results, err := s.mysqlSearch(q)
	return results, servedByMySQL, err
}

func (s *Service) SearchByType(docType, keyword string, page, size int, isAdmin bool) ([]SearchResult, response.Pagination, error) {
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
		if !isAdmin {
			tx = tx.Where("is_published = ?", true)
		}
		if keyword != "" {
			tx = tx.Where("title LIKE ? OR text LIKE ?", like, like)
		}
		if err := tx.Count(&total).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		var posts []models.PostModel
		if err := tx.
			Preload("Category").
			Order("created_at DESC").
			Offset(offset).
			Limit(size).
			Find(&posts).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		for _, p := range posts {
			tags := p.Tags
			if tags == nil {
				tags = []string{}
			}
			count := models.Count{Read: p.ReadCount, Like: p.LikeCount}
			isPublished := p.IsPublished
			copyright := p.Copyright
			pin := p.Pin
			pinOrder := p.PinOrder
			created := p.CreatedAt
			modified := p.UpdatedAt
			results = append(results, SearchResult{
				ID:          p.ID,
				Title:       p.Title,
				Summary:     p.Summary,
				Type:        "post",
				Slug:        p.Slug,
				Created:     &created,
				Modified:    &modified,
				CategoryID:  p.CategoryID,
				Category:    p.Category,
				Copyright:   &copyright,
				IsPublished: &isPublished,
				Tags:        tags,
				Count:       &count,
				Pin:         &pin,
				PinOrder:    &pinOrder,
				Images:      p.Images,
			})
		}

	case "note":
		tx := s.db.Model(&models.NoteModel{})
		if !isAdmin {
			tx = tx.Where("is_published = ?", true)
		}
		if keyword != "" {
			tx = tx.Where("title LIKE ? OR text LIKE ?", like, like)
		}
		if err := tx.Count(&total).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		var notes []models.NoteModel
		if err := tx.
			Preload("Topic").
			Order("created_at DESC").
			Offset(offset).
			Limit(size).
			Find(&notes).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		for _, n := range notes {
			count := models.Count{Read: n.ReadCount, Like: n.LikeCount}
			isPublished := n.IsPublished
			bookmark := n.Bookmark
			created := n.CreatedAt
			modified := n.UpdatedAt
			results = append(results, SearchResult{
				ID:          n.ID,
				Title:       n.Title,
				Type:        "note",
				NID:         n.NID,
				Created:     &created,
				Modified:    &modified,
				IsPublished: &isPublished,
				Mood:        n.Mood,
				Weather:     n.Weather,
				PublicAt:    n.PublicAt,
				Bookmark:    &bookmark,
				Coordinates: n.Coordinates,
				Location:    n.Location,
				Count:       &count,
				TopicID:     n.TopicID,
				Topic:       n.Topic,
				Images:      n.Images,
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
			Order("created_at DESC").
			Offset(offset).
			Limit(size).
			Find(&pages).Error; err != nil {
			return nil, response.Pagination{}, err
		}
		for _, pg := range pages {
			order := pg.Order
			allowComment := pg.AllowComment
			created := pg.CreatedAt
			modified := pg.UpdatedAt
			results = append(results, SearchResult{
				ID:           pg.ID,
				Title:        pg.Title,
				Type:         "page",
				Slug:         pg.Slug,
				Created:      &created,
				Modified:     &modified,
				Subtitle:     pg.Subtitle,
				Order:        &order,
				AllowComment: &allowComment,
				Meta:         pg.Meta,
				Images:       pg.Images,
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

	s.logger.Info(fmt.Sprintf("推送 %d 条文档到 MeiliSearch 索引...", len(docs)))
	if err := client.AddDocuments(docs); err != nil {
		s.logger.Warn("MeiliSearch 索引推送失败", zap.Error(err))
		return err
	}
	s.logger.Info("MeiliSearch 索引推送完成")
	return nil
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
