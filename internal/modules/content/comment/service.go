package comment

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query, refType *string, refID *string, state *int) ([]models.CommentModel, response.Pagination, error) {
	tx := s.db.Model(&models.CommentModel{}).
		Order("created_at DESC")

	if refType != nil {
		normalized := normalizeRefType(*refType)
		if normalized != "" {
			tx = tx.Where("ref_type = ?", normalized)
		}
	}
	if refID != nil {
		tx = tx.Where("ref_id = ?", *refID)
	}
	if state != nil {
		tx = tx.Where("state = ?", *state)
	}

	var comments []models.CommentModel
	pag, err := pagination.Paginate(tx, q, &comments)
	return comments, pag, err
}

func (s *Service) GetByID(id string) (*models.CommentModel, error) {
	var c models.CommentModel
	if err := s.db.Preload("Children").First(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *Service) Create(dto *CreateCommentDTO, ip, agent string) (*models.CommentModel, error) {
	refID := strings.TrimSpace(dto.RefID)
	refType := normalizeRefType(string(dto.RefType))
	if refID == "" || refType == "" {
		return nil, errCommentRefNotFound
	}

	tx := s.db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	commentsIndex, _, err := s.getRefCommentMeta(tx, refType, refID)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	c := models.CommentModel{
		RefType:  refType,
		RefID:    refID,
		Author:   dto.Author,
		Mail:     dto.Mail,
		URL:      dto.URL,
		Text:     dto.Text,
		ParentID: dto.ParentID,
		IP:       ip,
		Agent:    agent,
		Meta:     dto.Meta,
		State:    models.CommentUnread,
		Key:      fmt.Sprintf("#%d", commentsIndex+1),
	}
	if err := tx.Create(&c).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := s.incrementRefCommentsIndex(tx, refType, refID); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) Reply(parentID string, dto *CreateCommentDTO, ip, agent string) (*models.CommentModel, error) {
	tx := s.db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var parent models.CommentModel
	if err := tx.First(&parent, "id = ?", parentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			tx.Rollback()
			return nil, errCommentParentNotFound
		}
		tx.Rollback()
		return nil, err
	}
	if strings.TrimSpace(parent.Key) != "" && len(strings.Split(parent.Key, "#")) >= nestedReplyMax {
		tx.Rollback()
		return nil, errCommentTooDeep
	}

	parentKey := strings.TrimSpace(parent.Key)
	if parentKey == "" {
		parentKey = "#1"
	}
	c := models.CommentModel{
		RefType:  parent.RefType,
		RefID:    parent.RefID,
		Author:   dto.Author,
		Mail:     dto.Mail,
		URL:      dto.URL,
		Text:     dto.Text,
		ParentID: &parentID,
		IP:       ip,
		Agent:    agent,
		Meta:     dto.Meta,
		State:    models.CommentUnread,
		Key:      fmt.Sprintf("%s#%d", parentKey, parent.CommentsIndex),
	}
	if err := tx.Create(&c).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Model(&models.CommentModel{}).
		Where("id = ?", parentID).
		UpdateColumn("comments_index", gorm.Expr("comments_index + 1")).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) AllowComment(refType models.RefType, refID string) (bool, error) {
	_, allowComment, err := s.getRefCommentMeta(s.db, normalizeRefType(string(refType)), strings.TrimSpace(refID))
	return allowComment, err
}

func (s *Service) getRefCommentMeta(tx *gorm.DB, refType models.RefType, refID string) (int, bool, error) {
	switch refType {
	case models.RefTypePost:
		var post models.PostModel
		if err := tx.Select("id, comments_index, allow_comment").First(&post, "id = ?", refID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, false, errCommentRefNotFound
			}
			return 0, false, err
		}
		return post.CommentsIndex, post.AllowComment, nil
	case models.RefTypeNote:
		var note models.NoteModel
		if err := tx.Select("id, comments_index, allow_comment").First(&note, "id = ?", refID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, false, errCommentRefNotFound
			}
			return 0, false, err
		}
		return note.CommentsIndex, note.AllowComment, nil
	case models.RefTypePage:
		var page models.PageModel
		if err := tx.Select("id, comments_index, allow_comment").First(&page, "id = ?", refID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, false, errCommentRefNotFound
			}
			return 0, false, err
		}
		return page.CommentsIndex, page.AllowComment, nil
	case models.RefTypeRecently:
		var recently models.RecentlyModel
		if err := tx.Select("id, comments_index, allow_comment").First(&recently, "id = ?", refID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, false, errCommentRefNotFound
			}
			return 0, false, err
		}
		return recently.CommentsIndex, recently.AllowComment, nil
	default:
		return 0, false, errCommentRefNotFound
	}
}

func (s *Service) incrementRefCommentsIndex(tx *gorm.DB, refType models.RefType, refID string) error {
	switch refType {
	case models.RefTypePost:
		return tx.Model(&models.PostModel{}).Where("id = ?", refID).
			UpdateColumn("comments_index", gorm.Expr("comments_index + 1")).Error
	case models.RefTypeNote:
		return tx.Model(&models.NoteModel{}).Where("id = ?", refID).
			UpdateColumn("comments_index", gorm.Expr("comments_index + 1")).Error
	case models.RefTypePage:
		return tx.Model(&models.PageModel{}).Where("id = ?", refID).
			UpdateColumn("comments_index", gorm.Expr("comments_index + 1")).Error
	case models.RefTypeRecently:
		return tx.Model(&models.RecentlyModel{}).Where("id = ?", refID).
			UpdateColumn("comments_index", gorm.Expr("comments_index + 1")).Error
	default:
		return errCommentRefNotFound
	}
}

func (s *Service) ListByRef(refID string, q pagination.Query) ([]models.CommentModel, response.Pagination, error) {
	tx := s.db.Model(&models.CommentModel{}).
		Where("ref_id = ? AND parent_id IS NULL", refID).
		Preload("Children").
		Order("created_at DESC")
	var comments []models.CommentModel
	pag, err := pagination.Paginate(tx, q, &comments)
	return comments, pag, err
}

func (s *Service) UpdateState(id string, state models.CommentState) (*models.CommentModel, error) {
	c, err := s.GetByID(id)
	if err != nil || c == nil {
		return c, err
	}
	return c, s.db.Model(c).Update("state", state).Error
}

func (s *Service) Delete(id string) error {
	s.db.Where("parent_id = ?", id).Delete(&models.CommentModel{})
	return s.db.Delete(&models.CommentModel{}, "id = ?", id).Error
}
