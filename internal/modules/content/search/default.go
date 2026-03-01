package search

import (
	"github.com/mx-space/core/internal/models"
)

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
