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

type ListByRefOptions struct {
	IsAdmin       bool
	IncludeUnread bool
}

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
	if refID == "" {
		return nil, errCommentRefNotFound
	}
	if refType == "" {
		resolvedType, err := s.resolveRefTypeByID(refID)
		if err != nil {
			return nil, err
		}
		refType = resolvedType
	}
	if refType == "" {
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
		RefType:    refType,
		RefID:      refID,
		Author:     dto.Author,
		Mail:       dto.Mail,
		URL:        dto.URL,
		Text:       dto.Text,
		ParentID:   dto.ParentID,
		IP:         ip,
		Agent:      agent,
		Meta:       dto.Meta,
		IsWhispers: dto.IsWhisperEnabled(),
		State:      models.CommentUnread,
		Key:        fmt.Sprintf("#%d", commentsIndex+1),
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
		RefType:    parent.RefType,
		RefID:      parent.RefID,
		Author:     dto.Author,
		Mail:       dto.Mail,
		URL:        dto.URL,
		Text:       dto.Text,
		ParentID:   &parentID,
		IP:         ip,
		Agent:      agent,
		Meta:       dto.Meta,
		IsWhispers: parent.IsWhispers,
		State:      models.CommentUnread,
		Key:        fmt.Sprintf("%s#%d", parentKey, parent.CommentsIndex),
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
	refID = strings.TrimSpace(refID)
	if refID == "" {
		return false, errCommentRefNotFound
	}

	normalizedRefType := normalizeRefType(string(refType))
	if normalizedRefType == "" {
		resolvedType, err := s.resolveRefTypeByID(refID)
		if err != nil {
			return false, err
		}
		normalizedRefType = resolvedType
	}
	if normalizedRefType == "" {
		return false, errCommentRefNotFound
	}

	_, allowComment, err := s.getRefCommentMeta(s.db, normalizedRefType, refID)
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

func (s *Service) resolveRefTypeByID(refID string) (models.RefType, error) {
	refID = strings.TrimSpace(refID)
	if refID == "" {
		return "", nil
	}

	check := func(model interface{}, refType models.RefType) (models.RefType, error) {
		var count int64
		if err := s.db.Model(model).Where("id = ?", refID).Count(&count).Error; err != nil {
			return "", err
		}
		if count > 0 {
			return refType, nil
		}
		return "", nil
	}

	for _, item := range []struct {
		model   interface{}
		refType models.RefType
	}{
		{model: &models.PostModel{}, refType: models.RefTypePost},
		{model: &models.NoteModel{}, refType: models.RefTypeNote},
		{model: &models.PageModel{}, refType: models.RefTypePage},
		{model: &models.RecentlyModel{}, refType: models.RefTypeRecently},
	} {
		resolvedType, err := check(item.model, item.refType)
		if err != nil {
			return "", err
		}
		if resolvedType != "" {
			return resolvedType, nil
		}
	}
	return "", nil
}

type commentTreeNode struct {
	model    models.CommentModel
	children []*commentTreeNode
}

func (s *Service) ListByRef(refID string, q pagination.Query, opts ListByRefOptions) ([]models.CommentModel, response.Pagination, error) {
	refID = strings.TrimSpace(refID)
	tx := s.db.Model(&models.CommentModel{}).
		Where("ref_id = ?", refID).
		Order("pin DESC, created_at DESC")

	if opts.IncludeUnread {
		tx = tx.Where("state IN ?", []models.CommentState{models.CommentRead, models.CommentUnread})
	} else {
		tx = tx.Where("state = ?", models.CommentRead)
	}
	if !opts.IsAdmin {
		tx = tx.Where("is_whispers = ?", false)
	}

	var rows []models.CommentModel
	if err := tx.Find(&rows).Error; err != nil {
		return nil, response.Pagination{}, err
	}

	nodes := make([]*commentTreeNode, 0, len(rows))
	byID := make(map[string]*commentTreeNode, len(rows))
	byKey := make(map[string]*commentTreeNode, len(rows))

	for _, row := range rows {
		row.Children = nil
		node := &commentTreeNode{model: row}
		nodes = append(nodes, node)
		byID[node.model.ID] = node

		if key := strings.TrimSpace(node.model.Key); key != "" {
			byKey[parentLookupKey(node.model.RefType, node.model.RefID, key)] = node
		}
	}

	roots := make([]*commentTreeNode, 0, len(nodes))
	for _, node := range nodes {
		parent, hasParent := findCommentParentNode(node, byID, byKey)
		if parent == nil {
			if hasParent {
				// Dangling reply, skip to avoid broken tree output.
				continue
			}
			roots = append(roots, node)
			continue
		}
		if node.model.ParentID == nil {
			parentID := parent.model.ID
			node.model.ParentID = &parentID
		}
		parent.children = append(parent.children, node)
	}

	allRoots := make([]models.CommentModel, len(roots))
	for i, root := range roots {
		allRoots[i] = buildCommentTree(root)
	}

	total := len(allRoots)
	start := (q.Page - 1) * q.Size
	if start > total {
		start = total
	}
	end := start + q.Size
	if end > total {
		end = total
	}

	totalPage := 0
	if q.Size > 0 {
		totalPage = (total + q.Size - 1) / q.Size
	}

	pag := response.Pagination{
		Total:       int64(total),
		CurrentPage: q.Page,
		TotalPage:   totalPage,
		Size:        q.Size,
		HasNextPage: q.Page < totalPage,
	}
	return allRoots[start:end], pag, nil
}

func findCommentParentNode(node *commentTreeNode, byID map[string]*commentTreeNode, byKey map[string]*commentTreeNode) (*commentTreeNode, bool) {
	hasParent := false

	if node.model.ParentID != nil {
		parentID := strings.TrimSpace(*node.model.ParentID)
		if parentID != "" {
			hasParent = true
			if parent, ok := byID[parentID]; ok {
				return parent, true
			}
		}
	}

	if parentKey := parentKeyFromCommentKey(node.model.Key); parentKey != "" {
		hasParent = true
		if parent, ok := byKey[parentLookupKey(node.model.RefType, node.model.RefID, parentKey)]; ok {
			return parent, true
		}
	}

	return nil, hasParent
}

func buildCommentTree(node *commentTreeNode) models.CommentModel {
	comment := node.model
	if len(node.children) == 0 {
		comment.Children = nil
		return comment
	}
	children := make([]models.CommentModel, len(node.children))
	for i, child := range node.children {
		children[i] = buildCommentTree(child)
	}
	comment.Children = children
	return comment
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
