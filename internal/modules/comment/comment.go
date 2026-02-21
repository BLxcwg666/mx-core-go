package comment

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type CreateCommentDTO struct {
	RefType  models.RefType         `json:"ref_type"  binding:"required"`
	RefID    string                 `json:"ref_id"    binding:"required"`
	Author   string                 `json:"author"    binding:"required"`
	Mail     string                 `json:"mail"`
	URL      string                 `json:"url"`
	Text     string                 `json:"text"      binding:"required"`
	ParentID *string                `json:"parent_id"`
	Meta     map[string]interface{} `json:"meta"`
}

type UpdateCommentStateDTO struct {
	State models.CommentState `json:"state" binding:"required"`
}

type ReplyCommentDTO struct {
	Author string                 `json:"author"`
	Mail   string                 `json:"mail"`
	URL    string                 `json:"url"`
	Text   string                 `json:"text"   binding:"required"`
	Meta   map[string]interface{} `json:"meta"`
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) List(q pagination.Query, refType *string, refID *string, state *int) ([]models.CommentModel, response.Pagination, error) {
	tx := s.db.Model(&models.CommentModel{}).
		Where("parent_id IS NULL").
		Preload("Children").
		Order("created_at DESC")

	if refType != nil {
		tx = tx.Where("ref_type = ?", *refType)
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
	c := models.CommentModel{
		RefType:  dto.RefType,
		RefID:    dto.RefID,
		Author:   dto.Author,
		Mail:     dto.Mail,
		URL:      dto.URL,
		Text:     dto.Text,
		ParentID: dto.ParentID,
		IP:       ip,
		Agent:    agent,
		Meta:     dto.Meta,
		State:    models.CommentUnread,
	}
	return &c, s.db.Create(&c).Error
}

func (s *Service) Reply(parentID string, dto *CreateCommentDTO, ip, agent string) (*models.CommentModel, error) {
	parent, err := s.GetByID(parentID)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, fmt.Errorf("parent comment not found")
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
	}
	return &c, s.db.Create(&c).Error
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

type commentResponse struct {
	ID         string              `json:"id"`
	RefType    models.RefType      `json:"ref_type"`
	RefID      string              `json:"ref_id"`
	Author     string              `json:"author"`
	Mail       string              `json:"mail,omitempty"`
	URL        string              `json:"url"`
	Text       string              `json:"text"`
	State      models.CommentState `json:"state"`
	ParentID   *string             `json:"parent_id"`
	Children   []commentResponse   `json:"children"`
	IP         string              `json:"ip,omitempty"`
	Pin        bool                `json:"pin"`
	IsWhispers bool                `json:"is_whispers"`
	Avatar     string              `json:"avatar"`
	Location   string              `json:"location"`
	EditedAt   *time.Time          `json:"edited_at"`
	Created    time.Time           `json:"created"`
	Modified   time.Time           `json:"modified"`
}

func toResponse(c *models.CommentModel, isAdmin bool) commentResponse {
	children := make([]commentResponse, len(c.Children))
	for i, ch := range c.Children {
		children[i] = toResponse(&ch, isAdmin)
	}
	r := commentResponse{
		ID: c.ID, RefType: c.RefType, RefID: c.RefID,
		Author: c.Author, URL: c.URL, Text: c.Text,
		State: c.State, ParentID: c.ParentID, Children: children,
		Pin: c.Pin, IsWhispers: c.IsWhispers, Avatar: c.Avatar,
		Location: c.Location, EditedAt: c.EditedAt,
		Created: c.CreatedAt, Modified: c.UpdatedAt,
	}
	if isAdmin {
		r.IP = c.IP
		r.Mail = c.Mail
	}
	return r
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/comments")

	g.GET("/ref/:refId", h.listByRef)
	g.POST("/reply/:id", h.reply)
	g.POST("/owner/reply/:id", authMW, h.masterReply)
	g.POST("/master/reply/:id", authMW, h.masterReply)
	g.POST("/owner/comment/:id", authMW, h.masterComment)
	g.POST("/master/comment/:id", authMW, h.masterComment)

	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.POST("", h.create)
	g.POST("/:refId", h.createOnRef)

	a := g.Group("", authMW)
	a.PATCH("/batch/state", h.batchUpdateState)
	a.DELETE("/batch", h.batchDelete)
	a.PATCH("/edit/:id", h.edit)
	a.PATCH("/:id", h.updateStateCompat)
	a.PATCH("/:id/state", h.updateState)
	a.DELETE("/:id", h.delete)
}

func (h *Handler) list(c *gin.Context) {
	q := pagination.FromContext(c)

	refType := c.Query("ref_type")
	refID := c.Query("ref_id")

	var rtPtr, ridPtr *string
	if refType != "" {
		rtPtr = &refType
	}
	if refID != "" {
		ridPtr = &refID
	}

	var statePtr *int
	if state := c.Query("state"); state != "" {
		if parsed, err := strconv.Atoi(state); err == nil {
			statePtr = &parsed
		}
	}

	comments, pag, err := h.svc.List(q, rtPtr, ridPtr, statePtr)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	isAdmin := middleware.IsAuthenticated(c)
	items := make([]commentResponse, len(comments))
	for i, cm := range comments {
		items[i] = toResponse(&cm, isAdmin)
	}
	response.Paged(c, items, pag)
}

func (h *Handler) get(c *gin.Context) {
	cm, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cm == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(cm, middleware.IsAuthenticated(c)))
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateCommentDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	cm, err := h.svc.Create(&dto, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(cm, false))
}

func (h *Handler) updateState(c *gin.Context) {
	var dto UpdateCommentStateDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	cm, err := h.svc.UpdateState(c.Param("id"), dto.State)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cm == nil {
		response.NotFound(c)
		return
	}
	response.OK(c, toResponse(cm, true))
}

func (h *Handler) updateStateCompat(c *gin.Context) {
	h.updateState(c)
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) batchDelete(c *gin.Context) {
	var body struct {
		IDs []string `json:"ids" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	for _, id := range body.IDs {
		if id == "" {
			continue
		}
		if err := h.svc.Delete(id); err != nil {
			response.InternalError(c, err)
			return
		}
	}
	response.NoContent(c)
}

func (h *Handler) batchUpdateState(c *gin.Context) {
	var body struct {
		IDs   []string            `json:"ids" binding:"required,min=1"`
		State models.CommentState `json:"state" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	for _, id := range body.IDs {
		if id == "" {
			continue
		}
		if _, err := h.svc.UpdateState(id, body.State); err != nil {
			response.InternalError(c, err)
			return
		}
	}
	response.NoContent(c)
}

// GET /comments/ref/:refId
func (h *Handler) listByRef(c *gin.Context) {
	q := pagination.FromContext(c)
	comments, pag, err := h.svc.ListByRef(c.Param("refId"), q)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	isAdmin := middleware.IsAuthenticated(c)
	items := make([]commentResponse, len(comments))
	for i, cm := range comments {
		items[i] = toResponse(&cm, isAdmin)
	}
	response.Paged(c, items, pag)
}

// POST /comments/reply/:id — reply to a comment
func (h *Handler) reply(c *gin.Context) {
	var dto ReplyCommentDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	createDTO := &CreateCommentDTO{
		Author: dto.Author,
		Mail:   dto.Mail,
		URL:    dto.URL,
		Text:   dto.Text,
		Meta:   dto.Meta,
	}
	cm, err := h.svc.Reply(c.Param("id"), createDTO, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(cm, false))
}

// POST /comments/master/reply/:id - admin reply shortcut with implicit author.
func (h *Handler) masterReply(c *gin.Context) {
	var dto ReplyCommentDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if dto.Author == "" {
		userID := middleware.CurrentUserID(c)
		var user models.UserModel
		if err := h.svc.db.Select("name, mail, url").First(&user, "id = ?", userID).Error; err == nil {
			dto.Author = user.Name
			if dto.Mail == "" {
				dto.Mail = user.Mail
			}
			if dto.URL == "" {
				dto.URL = user.URL
			}
		}
		if dto.Author == "" {
			dto.Author = "Master"
		}
	}

	createDTO := &CreateCommentDTO{
		Author: dto.Author,
		Mail:   dto.Mail,
		URL:    dto.URL,
		Text:   dto.Text,
		Meta:   dto.Meta,
	}
	cm, err := h.svc.Reply(c.Param("id"), createDTO, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	_, _ = h.svc.UpdateState(cm.ID, models.CommentRead)
	response.Created(c, toResponse(cm, true))
}

// POST /comments/master/comment/:id or /comments/owner/comment/:id
func (h *Handler) masterComment(c *gin.Context) {
	refID := c.Param("id")
	var dto struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	userID := middleware.CurrentUserID(c)
	var user models.UserModel
	_ = h.svc.db.Select("name, mail, url").First(&user, "id = ?", userID).Error
	author := user.Name
	if author == "" {
		author = "Master"
	}

	createDTO := &CreateCommentDTO{
		RefID:   refID,
		RefType: models.RefTypePost,
		Author:  author,
		Mail:    user.Mail,
		URL:     user.URL,
		Text:    dto.Text,
	}
	if refType := c.Query("ref"); refType != "" {
		createDTO.RefType = models.RefType(refType)
	}
	cm, err := h.svc.Create(createDTO, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	_, _ = h.svc.UpdateState(cm.ID, models.CommentRead)
	response.Created(c, toResponse(cm, true))
}

// PATCH /comments/edit/:id
func (h *Handler) edit(c *gin.Context) {
	var body struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	now := time.Now()
	if err := h.svc.db.Model(&models.CommentModel{}).
		Where("id = ?", c.Param("id")).
		Updates(map[string]interface{}{
			"text":      body.Text,
			"edited_at": &now,
		}).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

// POST /comments/:refId — create comment on a ref (alternative to POST /comments)
func (h *Handler) createOnRef(c *gin.Context) {
	refID := c.Param("refId")
	var dto CreateCommentDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	dto.RefID = refID
	// RefType defaults to "post" if not provided
	if dto.RefType == "" {
		dto.RefType = "post"
	}
	cm, err := h.svc.Create(&dto, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, toResponse(cm, false))
}
