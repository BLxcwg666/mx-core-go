package comment

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	svc    *Service
	cfgSvc *appconfigs.Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{
		svc:    svc,
		cfgSvc: appconfigs.NewService(svc.db),
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/comments")

	g.GET("/ref/:refId", h.listByRef)
	g.POST("/reply/:id", h.reply)
	g.POST("/owner/reply/:id", authMW, h.masterReply)
	g.POST("/master/reply/:id", authMW, h.masterReply)
	g.POST("/owner/comment/:id", authMW, h.masterComment)
	g.POST("/master/comment/:id", authMW, h.masterComment)

	g.GET("", authMW, h.list)
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

func (h *Handler) isCommentDisabled() (bool, error) {
	if h.cfgSvc == nil {
		return false, nil
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return false, err
	}
	if cfg == nil {
		return false, nil
	}
	return cfg.CommentOptions.DisableComment, nil
}

func (h *Handler) ensureCommentEnabled(c *gin.Context) bool {
	disabled, err := h.isCommentDisabled()
	if err != nil {
		response.InternalError(c, err)
		return false
	}
	if disabled {
		response.ForbiddenMsg(c, "全站评论已关闭")
		return false
	}
	return true
}

func (h *Handler) ensureCommentAllowed(c *gin.Context, refType models.RefType, refID string) bool {
	allowComment, err := h.svc.AllowComment(refType, refID)
	if err != nil {
		if errors.Is(err, errCommentRefNotFound) {
			response.BadRequest(c, "评论文章不存在")
			return false
		}
		response.InternalError(c, err)
		return false
	}
	if !allowComment {
		response.ForbiddenMsg(c, "主人禁止了评论")
		return false
	}
	return true
}

func (h *Handler) handleCreateError(c *gin.Context, err error) bool {
	if errors.Is(err, errCommentRefNotFound) {
		response.BadRequest(c, "评论文章不存在")
		return true
	}
	return false
}

func (h *Handler) handleReplyError(c *gin.Context, err error) bool {
	if errors.Is(err, errCommentParentNotFound) {
		response.NotFoundMsg(c, "评论不存在")
		return true
	}
	if errors.Is(err, errCommentTooDeep) {
		response.BadRequest(c, "评论嵌套层数过深")
		return true
	}
	return false
}

func (h *Handler) loadParentMap(comments []models.CommentModel) (map[string]commentParentResponse, map[string]commentParentResponse, error) {
	parentIDs := make([]string, 0, len(comments))
	parentKeys := make([]string, 0, len(comments))
	for _, cm := range comments {
		if cm.ParentID != nil {
			parentIDs = append(parentIDs, *cm.ParentID)
			continue
		}
		if key := parentKeyFromCommentKey(cm.Key); key != "" {
			parentKeys = append(parentKeys, key)
		}
	}
	parentIDs = uniqueStrings(parentIDs)
	parentKeys = uniqueStrings(parentKeys)

	out := make(map[string]commentParentResponse, len(parentIDs))
	byKey := make(map[string]commentParentResponse)
	if len(parentIDs) == 0 {
		if len(parentKeys) == 0 {
			return out, byKey, nil
		}
	} else {
		var parents []models.CommentModel
		if err := h.svc.db.Select("id, author, text, created_at").
			Where("id IN ?", parentIDs).
			Find(&parents).Error; err != nil {
			return nil, nil, err
		}
		for _, p := range parents {
			out[p.ID] = commentParentResponse{
				ID:      p.ID,
				Author:  p.Author,
				Text:    p.Text,
				Created: p.CreatedAt,
			}
		}
	}

	if len(parentKeys) > 0 {
		var parentsByKey []models.CommentModel
		if err := h.svc.db.Select("id, ref_type, ref_id, `key`, author, text, created_at").
			Where("`key` IN ?", parentKeys).
			Find(&parentsByKey).Error; err != nil {
			return nil, nil, err
		}
		for _, p := range parentsByKey {
			byKey[parentLookupKey(p.RefType, p.RefID, p.Key)] = commentParentResponse{
				ID:      p.ID,
				Author:  p.Author,
				Text:    p.Text,
				Created: p.CreatedAt,
			}
		}
	}

	return out, byKey, nil
}

func (h *Handler) loadRefMap(comments []models.CommentModel) (map[string]gin.H, error) {
	postIDs := make([]string, 0)
	noteIDs := make([]string, 0)
	pageIDs := make([]string, 0)
	recentlyIDs := make([]string, 0)

	for _, cm := range comments {
		refID := strings.TrimSpace(cm.RefID)
		if refID == "" {
			continue
		}
		switch normalizeRefType(string(cm.RefType)) {
		case models.RefTypePost:
			postIDs = append(postIDs, refID)
		case models.RefTypeNote:
			noteIDs = append(noteIDs, refID)
		case models.RefTypePage:
			pageIDs = append(pageIDs, refID)
		case models.RefTypeRecently:
			recentlyIDs = append(recentlyIDs, refID)
		}
	}

	postIDs = uniqueStrings(postIDs)
	noteIDs = uniqueStrings(noteIDs)
	pageIDs = uniqueStrings(pageIDs)
	recentlyIDs = uniqueStrings(recentlyIDs)

	out := make(map[string]gin.H, len(postIDs)+len(noteIDs)+len(pageIDs)+len(recentlyIDs))

	if len(postIDs) > 0 {
		var posts []models.PostModel
		if err := h.svc.db.Preload("Category").Where("id IN ?", postIDs).Find(&posts).Error; err != nil {
			return nil, err
		}
		for _, post := range posts {
			out[refMapKey(models.RefTypePost, post.ID)] = compactPostRef(post)
		}
	}

	if len(noteIDs) > 0 {
		var notes []models.NoteModel
		if err := h.svc.db.Where("id IN ?", noteIDs).Find(&notes).Error; err != nil {
			return nil, err
		}
		for _, note := range notes {
			out[refMapKey(models.RefTypeNote, note.ID)] = compactNoteRef(note)
		}
	}

	if len(pageIDs) > 0 {
		var pages []models.PageModel
		if err := h.svc.db.Where("id IN ?", pageIDs).Find(&pages).Error; err != nil {
			return nil, err
		}
		for _, page := range pages {
			out[refMapKey(models.RefTypePage, page.ID)] = compactPageRef(page)
		}
	}

	if len(recentlyIDs) > 0 {
		var recentlies []models.RecentlyModel
		if err := h.svc.db.Where("id IN ?", recentlyIDs).Find(&recentlies).Error; err != nil {
			return nil, err
		}
		for _, recently := range recentlies {
			out[refMapKey(models.RefTypeRecently, recently.ID)] = compactRecentlyRef(recently)
		}
	}

	return out, nil
}

func (h *Handler) loadReadersMap(comments []models.CommentModel) (gin.H, error) {
	readerIDs := make([]string, 0, len(comments))
	for _, cm := range comments {
		if cm.ReaderID != nil {
			readerIDs = append(readerIDs, *cm.ReaderID)
		}
	}
	readerIDs = uniqueStrings(readerIDs)
	readers := gin.H{}
	if len(readerIDs) == 0 {
		return readers, nil
	}

	var rows []models.ReaderModel
	if err := h.svc.db.Where("id IN ?", readerIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		readers[row.ID] = row
	}
	return readers, nil
}

func (h *Handler) fillAvatarForComment(cm *models.CommentModel) {
	if strings.TrimSpace(cm.Avatar) != "" {
		return
	}

	var user models.UserModel
	if err := h.svc.db.Select("avatar").Where("name = ?", cm.Author).First(&user).Error; err == nil {
		if avatar := strings.TrimSpace(user.Avatar); avatar != "" {
			cm.Avatar = avatar
			return
		}
	}

	mail := strings.ToLower(strings.TrimSpace(cm.Mail))
	if mail == "" {
		return
	}
	sum := md5.Sum([]byte(mail))
	cm.Avatar = "https://avatar.xcnya.cn/avatar/" + hex.EncodeToString(sum[:]) + "?d=retro"
}

func (h *Handler) fillAvatarTree(cm *models.CommentModel) {
	h.fillAvatarForComment(cm)
	for i := range cm.Children {
		h.fillAvatarTree(&cm.Children[i])
	}
}

func legacyCommentPayload(cm *models.CommentModel, isAdmin bool) gin.H {
	children := make([]gin.H, 0, len(cm.Children))
	for i := range cm.Children {
		children = append(children, legacyCommentPayload(&cm.Children[i], isAdmin))
	}

	item := gin.H{
		"id":             cm.ID,
		"ref":            cm.RefID,
		"ref_type":       refTypeForResponse(cm.RefType),
		"author":         cm.Author,
		"text":           cm.Text,
		"state":          cm.State,
		"children":       children,
		"comments_index": cm.CommentsIndex,
		"key":            cm.Key,
		"pin":            cm.Pin,
		"is_whispers":    cm.IsWhispers,
		"created":        cm.CreatedAt,
		"avatar":         cm.Avatar,
	}

	if v := strings.TrimSpace(cm.URL); v != "" {
		item["url"] = v
	}
	if v := strings.TrimSpace(cm.Source); v != "" {
		item["source"] = v
	}
	if cm.ParentID != nil {
		item["parent"] = *cm.ParentID
	}
	if isAdmin {
		item["mail"] = cm.Mail
		item["ip"] = cm.IP
		item["agent"] = cm.Agent
	}
	if cm.EditedAt != nil {
		item["edited_at"] = cm.EditedAt
	}

	return item
}

func (h *Handler) buildReplyPayload(commentID string, isAdmin bool) (gin.H, error) {
	var cm models.CommentModel
	if err := h.svc.db.First(&cm, "id = ?", commentID).Error; err != nil {
		return nil, err
	}
	h.fillAvatarForComment(&cm)

	payload := legacyCommentPayload(&cm, isAdmin)

	refMap, err := h.loadRefMap([]models.CommentModel{cm})
	if err != nil {
		return nil, err
	}
	if ref, ok := refMap[refMapKey(cm.RefType, cm.RefID)]; ok {
		payload["ref"] = ref
	}

	var parent models.CommentModel
	parentFound := false
	if cm.ParentID != nil && strings.TrimSpace(*cm.ParentID) != "" {
		if err := h.svc.db.Preload("Children").First(&parent, "id = ?", *cm.ParentID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
		} else {
			parentFound = true
		}
	} else if parentKey := parentKeyFromCommentKey(cm.Key); parentKey != "" {
		if err := h.svc.db.Preload("Children").
			Where("ref_type = ? AND ref_id = ? AND `key` = ?", normalizeRefType(string(cm.RefType)), cm.RefID, parentKey).
			First(&parent).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
		} else {
			parentFound = true
		}
	}
	if parentFound {
		h.fillAvatarTree(&parent)
		payload["parent"] = legacyCommentPayload(&parent, isAdmin)
	}

	return payload, nil
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

	defaultState := int(models.CommentUnread)
	statePtr := &defaultState
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
	parentMap, parentByKey, err := h.loadParentMap(comments)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	refMap, err := h.loadRefMap(comments)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	readers, err := h.loadReadersMap(comments)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	items := make([]commentResponse, len(comments))
	for i, cm := range comments {
		h.fillAvatarTree(&cm)
		item := toResponse(&cm, isAdmin)
		if cm.ParentID != nil {
			if parent, ok := parentMap[*cm.ParentID]; ok {
				item.Parent = parent
			}
		} else if parentKey := parentKeyFromCommentKey(cm.Key); parentKey != "" {
			if parent, ok := parentByKey[parentLookupKey(cm.RefType, cm.RefID, parentKey)]; ok {
				item.Parent = parent
			}
		}
		if ref, ok := refMap[refMapKey(cm.RefType, cm.RefID)]; ok {
			item.Ref = ref
		}
		items[i] = item
	}
	c.JSON(200, gin.H{
		"data":       items,
		"pagination": pag,
		"readers":    readers,
	})
}

func (h *Handler) get(c *gin.Context) {
	cm, err := h.svc.GetByID(c.Param("id"))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cm == nil {
		response.NotFoundMsg(c, "评论不存在")
		return
	}
	h.fillAvatarTree(cm)
	response.OK(c, toResponse(cm, middleware.IsAuthenticated(c)))
}

func (h *Handler) create(c *gin.Context) {
	var dto CreateCommentDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !h.ensureCommentEnabled(c) {
		return
	}
	if !middleware.IsAuthenticated(c) && !h.ensureCommentAllowed(c, dto.RefType, dto.RefID) {
		return
	}
	cm, err := h.svc.Create(&dto, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		if h.handleCreateError(c, err) {
			return
		}
		response.InternalError(c, err)
		return
	}
	h.fillAvatarForComment(cm)
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
		response.NotFoundMsg(c, "评论不存在")
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
	readers, err := h.loadReadersMap(comments)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	items := make([]commentResponse, len(comments))
	for i, cm := range comments {
		h.fillAvatarTree(&cm)
		items[i] = toResponse(&cm, isAdmin)
	}
	c.JSON(200, gin.H{
		"data":       items,
		"pagination": pag,
		"readers":    readers,
	})
}

// POST /comments/reply/:id — reply to a comment
func (h *Handler) reply(c *gin.Context) {
	var dto ReplyCommentDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !h.ensureCommentEnabled(c) {
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
		if h.handleReplyError(c, err) {
			return
		}
		response.InternalError(c, err)
		return
	}
	payload, err := h.buildReplyPayload(cm.ID, false)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, payload)
}

// POST /comments/master/reply/:id - admin reply shortcut with implicit author.
func (h *Handler) masterReply(c *gin.Context) {
	var dto ReplyCommentDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !h.ensureCommentEnabled(c) {
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
		if h.handleReplyError(c, err) {
			return
		}
		response.InternalError(c, err)
		return
	}
	_, _ = h.svc.UpdateState(cm.ID, models.CommentRead)
	payload, err := h.buildReplyPayload(cm.ID, true)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, payload)
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
	if !h.ensureCommentEnabled(c) {
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
		createDTO.RefType = normalizeRefType(refType)
	}
	cm, err := h.svc.Create(createDTO, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		if h.handleCreateError(c, err) {
			return
		}
		response.InternalError(c, err)
		return
	}
	_, _ = h.svc.UpdateState(cm.ID, models.CommentRead)
	h.fillAvatarForComment(cm)
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
	if !h.ensureCommentEnabled(c) {
		return
	}
	dto.RefID = refID
	// RefType defaults to "post" if not provided
	if dto.RefType == "" {
		dto.RefType = models.RefTypePost
	} else {
		dto.RefType = normalizeRefType(string(dto.RefType))
	}
	if !middleware.IsAuthenticated(c) && !h.ensureCommentAllowed(c, dto.RefType, dto.RefID) {
		return
	}
	cm, err := h.svc.Create(&dto, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		if h.handleCreateError(c, err) {
			return
		}
		response.InternalError(c, err)
		return
	}
	h.fillAvatarForComment(cm)
	response.Created(c, toResponse(cm, false))
}
