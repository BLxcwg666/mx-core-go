package note

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) List(q pagination.Query) ([]models.NoteModel, response.Pagination, error) {
	tx := s.db.Model(&models.NoteModel{}).
		Preload("Topic").
		Order("created_at DESC")

	var notes []models.NoteModel
	pag, err := pagination.Paginate(tx, q, &notes)
	return notes, pag, err
}

func (s *Service) GetByNID(nid int, isAdmin bool) (*models.NoteModel, error) {
	var note models.NoteModel
	tx := s.db.Preload("Topic").Where("n_id = ?", nid)
	if !isAdmin {
		tx = tx.Where("is_published = ?", true)
	}
	if err := tx.First(&note).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &note, nil
}

func (s *Service) GetByID(id string) (*models.NoteModel, error) {
	var note models.NoteModel
	if err := s.db.Preload("Topic").First(&note, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &note, nil
}

func (s *Service) GetLatest(isAdmin bool) (*models.NoteModel, error) {
	var note models.NoteModel
	tx := s.db.Preload("Topic").Order("created_at DESC")
	if !isAdmin {
		tx = tx.Where("is_published = ?", true)
	}
	if err := tx.First(&note).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &note, nil
}

func (s *Service) ListByTopic(topicID string, q pagination.Query, isAdmin bool) ([]models.NoteModel, response.Pagination, error) {
	tx := s.db.Model(&models.NoteModel{}).
		Preload("Topic").
		Where("topic_id = ?", topicID).
		Order("created_at DESC")
	if !isAdmin {
		tx = tx.Where("is_published = ?", true)
	}

	var notes []models.NoteModel
	pag, err := pagination.Paginate(tx, q, &notes)
	return notes, pag, err
}

// ListAround returns notes around the given note id, including itself.
func (s *Service) ListAround(id string, size int, isAdmin bool) ([]models.NoteModel, error) {
	if size <= 0 {
		size = 10
	}

	current, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return []models.NoteModel{}, nil
	}

	if !isAdmin && !current.IsPublished {
		return []models.NoteModel{}, nil
	}

	limit := size/2 - 1
	if limit < 0 {
		limit = 0
	}

	base := s.db.Model(&models.NoteModel{}).Select("id, n_id, title, is_published, created_at, updated_at")
	if !isAdmin {
		base = base.Where("is_published = ?", true)
	}

	prev := make([]models.NoteModel, 0, limit)
	if limit > 0 {
		if err := base.
			Where("created_at > ?", current.CreatedAt).
			Order("created_at ASC").
			Limit(limit).
			Find(&prev).Error; err != nil {
			return nil, err
		}
	}

	next := make([]models.NoteModel, 0, limit)
	if limit > 0 {
		if err := base.
			Where("created_at < ?", current.CreatedAt).
			Order("created_at DESC").
			Limit(limit).
			Find(&next).Error; err != nil {
			return nil, err
		}
	}

	items := make([]models.NoteModel, 0, len(prev)+len(next)+1)
	items = append(items, prev...)
	items = append(items, next...)
	items = append(items, *current)

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	return items, nil
}

func (s *Service) nextNID(db *gorm.DB) (int, error) {
	if db == nil {
		db = s.db
	}
	var maxNID int
	// Include soft-deleted rows because the unique index on n_id still keeps them.
	if err := db.Unscoped().Model(&models.NoteModel{}).Select("COALESCE(MAX(n_id), 0)").Scan(&maxNID).Error; err != nil {
		return 0, err
	}
	return maxNID + 1, nil
}

func (s *Service) Create(dto *CreateNoteDTO) (*models.NoteModel, error) {
	const maxCreateRetries = 5
	for i := 0; i < maxCreateRetries; i++ {
		nid, err := s.nextNID(nil)
		if err != nil {
			return nil, err
		}

		note := models.NoteModel{
			WriteBase: models.WriteBase{
				Title:  dto.Title,
				Text:   dto.Text,
				Images: dto.Images,
			},
			NID:         nid,
			PublicAt:    dto.PublicAt,
			Mood:        dto.Mood,
			Weather:     dto.Weather,
			Coordinates: dto.Coordinates,
			Location:    dto.Location,
			TopicID:     dto.TopicID,
		}
		if dto.IsPublished != nil {
			note.IsPublished = *dto.IsPublished
		}
		if dto.Bookmark != nil {
			note.Bookmark = *dto.Bookmark
		}
		if dto.Password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(dto.Password), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}
			note.Password = string(hash)
		}

		if err := s.db.Create(&note).Error; err != nil {
			if isDuplicateNIDError(err) && i < maxCreateRetries-1 {
				continue
			}
			return nil, err
		}
		return &note, nil
	}

	return nil, fmt.Errorf("failed to allocate note nid after retries")
}

func isDuplicateNIDError(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		if mysqlErr.Number == 1062 &&
			(strings.Contains(mysqlErr.Message, "idx_notes_n_id") ||
				strings.Contains(mysqlErr.Message, "notes.n_id")) {
			return true
		}
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") && strings.Contains(msg, "n_id")
}

func (s *Service) Update(id string, dto *UpdateNoteDTO) (*models.NoteModel, error) {
	note, err := s.GetByID(id)
	if err != nil || note == nil {
		return note, err
	}

	updates := map[string]interface{}{}
	if dto.Title != nil {
		updates["title"] = *dto.Title
	}
	if dto.Text != nil {
		updates["text"] = *dto.Text
	}
	if dto.IsPublished != nil {
		updates["is_published"] = *dto.IsPublished
	}
	if dto.Mood != nil {
		updates["mood"] = *dto.Mood
	}
	if dto.Weather != nil {
		updates["weather"] = *dto.Weather
	}
	if dto.Bookmark != nil {
		updates["bookmark"] = *dto.Bookmark
	}
	if dto.Location != nil {
		updates["location"] = *dto.Location
	}
	if dto.TopicID != nil {
		updates["topic_id"] = *dto.TopicID
	}
	if dto.PublicAt != nil {
		updates["public_at"] = dto.PublicAt
	}
	if dto.Coordinates != nil {
		updates["coordinates"] = dto.Coordinates
	}
	if dto.Images != nil {
		updates["images"] = dto.Images
	}
	if dto.Password != nil {
		if *dto.Password == "" {
			updates["password_hash"] = ""
		} else {
			hash, err := bcrypt.GenerateFromPassword([]byte(*dto.Password), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}
			updates["password_hash"] = string(hash)
		}
	}

	if err := s.db.Model(note).Updates(updates).Error; err != nil {
		return nil, err
	}
	return note, nil
}

func (s *Service) Delete(id string) error {
	return s.db.Delete(&models.NoteModel{}, "id = ?", id).Error
}

func (s *Service) IncrementReadCount(id string) error {
	return s.db.Model(&models.NoteModel{}).Where("id = ?", id).
		UpdateColumn("read_count", gorm.Expr("read_count + 1")).Error
}

func (s *Service) IncrementLikeCount(id string) error {
	return s.db.Model(&models.NoteModel{}).Where("id = ?", id).
		UpdateColumn("like_count", gorm.Expr("like_count + 1")).Error
}
