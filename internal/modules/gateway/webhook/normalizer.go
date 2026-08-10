package webhook

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

func (s *Service) normalizePayload(event string, payload interface{}, scope int) interface{} {
	switch event {
	case "POST_CREATE", "POST_UPDATE":
		if post, err := s.loadPost(payload); err == nil && post != nil {
			return buildWebhookPostPayload(post)
		}
	case "CATEGORY_CREATE", "CATEGORY_UPDATE":
		if category, err := s.loadCategory(payload); err == nil && category != nil {
			return buildWebhookCategoryPayload(category)
		}
	case "TOPIC_CREATE", "TOPIC_UPDATE":
		if topic, err := s.loadTopic(payload); err == nil && topic != nil {
			return buildWebhookTopicPayload(topic)
		}
	case "NOTE_CREATE", "NOTE_UPDATE":
		if note, err := s.loadNote(payload); err == nil && note != nil {
			return buildWebhookNotePayload(note)
		}
	case "PAGE_CREATE", "PAGE_UPDATE":
		if page, err := s.loadPage(payload); err == nil && page != nil {
			return buildWebhookPagePayload(page)
		}
	case "SAY_CREATE", "SAY_UPDATE":
		if say, err := s.loadSay(payload); err == nil && say != nil {
			return buildWebhookSayPayload(say)
		}
	case "RECENTLY_CREATE", "RECENTLY_UPDATE":
		if recently, err := s.loadRecently(payload); err == nil && recently != nil {
			return s.buildWebhookRecentlyPayload(recently)
		}
	case "COMMENT_CREATE":
		if comment, err := s.loadComment(payload); err == nil && comment != nil {
			includePrivate := scope&ScopeToAdmin != 0 && scope&ScopeToVisitor == 0
			return s.buildWebhookCommentPayload(comment, includePrivate)
		}
	case "LINK_APPLY":
		if link, err := s.loadLink(payload); err == nil && link != nil {
			return buildWebhookLinkPayload(link)
		}
	case "ACTIVITY_LIKE":
		return s.normalizeActivityLikePayload(payload)
	case "POST_DELETE", "NOTE_DELETE", "PAGE_DELETE", "SAY_DELETE":
		if id := extractPayloadID(payload); id != "" {
			return map[string]interface{}{"data": id}
		}
	}

	return payload
}

func (s *Service) loadPost(payload interface{}) (*models.PostModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var post models.PostModel
	if err := s.db.Preload("Category").Preload("Related").First(&post, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &post, nil
}

func (s *Service) loadNote(payload interface{}) (*models.NoteModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var note models.NoteModel
	if err := s.db.Preload("Topic").First(&note, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &note, nil
}

func (s *Service) loadCategory(payload interface{}) (*models.CategoryModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var category models.CategoryModel
	if err := s.db.First(&category, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &category, nil
}

func (s *Service) loadTopic(payload interface{}) (*models.TopicModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var topic models.TopicModel
	if err := s.db.First(&topic, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &topic, nil
}

func (s *Service) loadPage(payload interface{}) (*models.PageModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var page models.PageModel
	if err := s.db.First(&page, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &page, nil
}

func (s *Service) loadSay(payload interface{}) (*models.SayModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var say models.SayModel
	if err := s.db.First(&say, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &say, nil
}

func (s *Service) loadRecently(payload interface{}) (*models.RecentlyModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var recently models.RecentlyModel
	if err := s.db.First(&recently, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &recently, nil
}

func (s *Service) loadComment(payload interface{}) (*models.CommentModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var comment models.CommentModel
	if err := s.db.First(&comment, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &comment, nil
}

func (s *Service) loadLink(payload interface{}) (*models.LinkModel, error) {
	id := extractPayloadID(payload)
	if id == "" {
		return nil, nil
	}
	var link models.LinkModel
	if err := s.db.First(&link, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &link, nil
}

func (s *Service) loadReader(id string) (*models.ReaderModel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	var reader models.ReaderModel
	if err := s.db.First(&reader, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &reader, nil
}

func buildWebhookPostPayload(post *models.PostModel) map[string]interface{} {
	tags := post.Tags
	if tags == nil {
		tags = []string{}
	}
	images := post.Images
	if images == nil {
		images = []models.Image{}
	}
	related := make([]map[string]interface{}, 0, len(post.Related))
	relatedIDs := make([]string, 0, len(post.Related))
	for _, item := range post.Related {
		relatedIDs = append(relatedIDs, item.ID)
		related = append(related, map[string]interface{}{
			"id":    item.ID,
			"title": item.Title,
			"slug":  item.Slug,
		})
	}
	payload := map[string]interface{}{
		"id":            post.ID,
		"title":         post.Title,
		"text":          post.Text,
		"contentFormat": "markdown",
		"images":        images,
		"modified":      models.NullableModified(post.CreatedAt, post.UpdatedAt),
		"created":       post.CreatedAt,
		"slug":          post.Slug,
		"summary":       nullableString(post.Summary),
		"categoryId":    derefString(post.CategoryID),
		"copyright":     post.Copyright,
		"isPublished":   post.IsPublished,
		"tags":          tags,
		"count":         post.GetCount(),
		"allowComment":  post.AllowComment,
		"commentsIndex": post.CommentsIndex,
		"relatedId":     relatedIDs,
		"related":       related,
		"pin":           post.Pin,
	}
	if post.Category != nil {
		payload["category"] = buildWebhookCategoryPayload(post.Category)
	}
	if post.PinOrder != 0 {
		payload["pinOrder"] = post.PinOrder
	}
	return payload
}

func buildWebhookNotePayload(note *models.NoteModel) map[string]interface{} {
	images := note.Images
	if images == nil {
		images = []models.Image{}
	}
	payload := map[string]interface{}{
		"id":            note.ID,
		"title":         note.Title,
		"text":          note.Text,
		"contentFormat": "markdown",
		"images":        images,
		"modified":      models.NullableModified(note.CreatedAt, note.UpdatedAt),
		"created":       note.CreatedAt,
		"nid":           note.NID,
		"isPublished":   note.IsPublished,
		"publicAt":      note.PublicAt,
		"mood":          note.Mood,
		"weather":       note.Weather,
		"bookmark":      note.Bookmark,
		"coordinates":   note.Coordinates,
		"location":      note.Location,
		"count": map[string]interface{}{
			"read": note.ReadCount,
			"like": note.LikeCount,
		},
		"allowComment":  note.AllowComment,
		"commentsIndex": note.CommentsIndex,
	}
	if strings.TrimSpace(note.Password) != "" {
		payload["text"] = ""
	}
	if note.TopicID != nil {
		payload["topicId"] = *note.TopicID
	}
	if note.Topic != nil {
		payload["topic"] = buildWebhookTopicPayload(note.Topic)
	}
	return payload
}

func buildWebhookPagePayload(page *models.PageModel) map[string]interface{} {
	images := page.Images
	if images == nil {
		images = []models.Image{}
	}
	return map[string]interface{}{
		"id":            page.ID,
		"title":         page.Title,
		"text":          page.Text,
		"contentFormat": "markdown",
		"images":        images,
		"modified":      models.NullableModified(page.CreatedAt, page.UpdatedAt),
		"created":       page.CreatedAt,
		"meta":          page.Meta,
		"allowComment":  page.AllowComment,
		"commentsIndex": page.CommentsIndex,
		"slug":          page.Slug,
		"subtitle":      nullableString(page.Subtitle),
		"order":         page.Order,
	}
}

func buildWebhookSayPayload(say *models.SayModel) map[string]interface{} {
	return map[string]interface{}{
		"id":      say.ID,
		"text":    say.Text,
		"source":  say.Source,
		"author":  say.Author,
		"created": say.CreatedAt,
	}
}

func buildWebhookCategoryPayload(category *models.CategoryModel) map[string]interface{} {
	return map[string]interface{}{
		"id":      category.ID,
		"created": category.CreatedAt,
		"name":    category.Name,
		"type":    category.Type,
		"slug":    category.Slug,
	}
}

func buildWebhookTopicPayload(topic *models.TopicModel) map[string]interface{} {
	payload := map[string]interface{}{
		"id":          topic.ID,
		"created":     topic.CreatedAt,
		"description": topic.Description,
		"introduce":   topic.Introduce,
		"name":        topic.Name,
		"slug":        topic.Slug,
	}
	if strings.TrimSpace(topic.Icon) != "" {
		payload["icon"] = topic.Icon
	}
	return payload
}

func buildWebhookLinkPayload(link *models.LinkModel) map[string]interface{} {
	payload := map[string]interface{}{
		"id":          link.ID,
		"created":     link.CreatedAt,
		"name":        link.Name,
		"url":         link.URL,
		"description": link.Description,
		"type":        link.Type,
		"state":       link.State,
	}
	if strings.TrimSpace(link.Avatar) != "" {
		payload["avatar"] = link.Avatar
	}
	if strings.TrimSpace(link.Email) != "" {
		payload["email"] = link.Email
	}
	return payload
}

func (s *Service) buildWebhookRecentlyPayload(recently *models.RecentlyModel) map[string]interface{} {
	payload := map[string]interface{}{
		"id":            recently.ID,
		"created":       recently.CreatedAt,
		"content":       recently.Content,
		"type":          normalizeWebhookRecentlyType(recently.Type),
		"allowComment":  recently.AllowComment,
		"commentsIndex": recently.CommentsIndex,
		"modified":      models.NullableModified(recently.CreatedAt, recently.UpdatedAt),
		"up":            recently.UpCount,
		"down":          recently.DownCount,
	}
	if recently.Metadata != nil {
		payload["metadata"] = recently.Metadata
	}
	if recently.RefType != nil {
		payload["refType"] = collectionRefType(*recently.RefType)
	}
	if ref := s.buildWebhookRecentlyRef(recently); len(ref) > 0 {
		payload["ref"] = ref
	}
	return payload
}

func (s *Service) buildWebhookRecentlyRef(recently *models.RecentlyModel) map[string]interface{} {
	if recently.RefType == nil || recently.RefID == nil || strings.TrimSpace(*recently.RefID) == "" {
		return nil
	}
	refID := strings.TrimSpace(*recently.RefID)
	switch *recently.RefType {
	case models.RefTypePost:
		var post models.PostModel
		if err := s.db.Preload("Category").First(&post, "id = ?", refID).Error; err == nil && post.Category != nil {
			return map[string]interface{}{
				"title": post.Title,
				"url":   "/posts/" + post.Category.Slug + "/" + post.Slug,
			}
		}
	case models.RefTypeNote:
		var note models.NoteModel
		if err := s.db.First(&note, "id = ?", refID).Error; err == nil {
			return map[string]interface{}{
				"title": note.Title,
				"url":   "/notes/" + strconv.Itoa(note.NID),
			}
		}
	case models.RefTypePage:
		var page models.PageModel
		if err := s.db.First(&page, "id = ?", refID).Error; err == nil {
			return map[string]interface{}{
				"title": page.Title,
				"url":   "/" + strings.TrimPrefix(page.Slug, "/"),
			}
		}
	case models.RefTypeRecently:
		var refRecently models.RecentlyModel
		if err := s.db.First(&refRecently, "id = ?", refID).Error; err == nil {
			return map[string]interface{}{
				"title": refRecently.Content,
				"url":   "/timeline",
			}
		}
	}
	return nil
}

func (s *Service) buildWebhookCommentPayload(comment *models.CommentModel, includePrivate bool) map[string]interface{} {
	payload := map[string]interface{}{
		"id":         comment.ID,
		"created":    comment.CreatedAt,
		"ref":        comment.RefID,
		"refType":    collectionRefType(comment.RefType),
		"author":     comment.Author,
		"text":       comment.Text,
		"state":      comment.State,
		"pin":        comment.Pin,
		"isWhispers": comment.IsWhispers,
	}
	attachCommentRefField(payload, comment.RefType, comment.RefID)
	if comment.ParentID != nil {
		payload["parentCommentId"] = *comment.ParentID
	}
	if v := strings.TrimSpace(comment.URL); v != "" {
		payload["url"] = v
	}
	if includePrivate {
		payload["mail"] = comment.Mail
		if v := strings.TrimSpace(comment.IP); v != "" {
			payload["ip"] = v
		}
		if v := strings.TrimSpace(comment.Agent); v != "" {
			payload["agent"] = v
		}
	}
	if v := strings.TrimSpace(comment.Location); v != "" {
		payload["location"] = v
	}
	if v := strings.TrimSpace(comment.Avatar); v != "" {
		payload["avatar"] = v
	}
	if comment.ReaderID != nil {
		payload["readerId"] = *comment.ReaderID
	}
	if comment.EditedAt != nil {
		payload["editedAt"] = comment.EditedAt
	}
	if comment.Meta != nil {
		payload["meta"] = mustJSONString(comment.Meta)
	}
	if comment.ReaderID != nil && strings.TrimSpace(*comment.ReaderID) != "" {
		s.enrichCommentIdentity(payload, *comment.ReaderID)
	}
	if rootID, replyCount, latestReplyAt := s.commentThreadInfo(comment); rootID != "" {
		payload["rootCommentId"] = rootID
		payload["replyCount"] = replyCount
		if latestReplyAt != nil {
			payload["latestReplyAt"] = latestReplyAt
		}
	}
	return payload
}

func attachCommentRefField(payload map[string]interface{}, refType models.RefType, refID string) {
	if strings.TrimSpace(refID) == "" {
		return
	}
	switch refType {
	case models.RefTypePost:
		payload["post"] = refID
	case models.RefTypeNote:
		payload["note"] = refID
	case models.RefTypePage:
		payload["page"] = refID
	case models.RefTypeRecently:
		payload["recently"] = refID
	}
}

func (s *Service) commentThreadInfo(comment *models.CommentModel) (string, int64, *time.Time) {
	if comment == nil {
		return "", 0, nil
	}
	rootID := comment.ID
	if s == nil || s.db == nil {
		return rootID, 0, nil
	}
	current := comment
	for current.ParentID != nil && strings.TrimSpace(*current.ParentID) != "" {
		parentID := strings.TrimSpace(*current.ParentID)
		var parent models.CommentModel
		if err := s.db.Select("id, parent_id").First(&parent, "id = ?", parentID).Error; err != nil {
			break
		}
		rootID = parent.ID
		current = &parent
	}

	query := s.db.Model(&models.CommentModel{}).
		Where("ref_type = ? AND ref_id = ? AND id <> ?", comment.RefType, comment.RefID, comment.ID)
	if key := strings.TrimSpace(comment.Key); key != "" {
		query = query.Where("`key` LIKE ?", key+"#%")
	} else {
		query = query.Where("parent_id = ?", comment.ID)
	}

	var replyCount int64
	query.Count(&replyCount)
	if replyCount == 0 {
		return rootID, 0, nil
	}
	var latest struct {
		CreatedAt time.Time `gorm:"column:created_at"`
	}
	if err := query.Select("MAX(created_at) AS created_at").Scan(&latest).Error; err != nil || latest.CreatedAt.IsZero() {
		return rootID, replyCount, nil
	}
	return rootID, replyCount, &latest.CreatedAt
}

func (s *Service) enrichCommentIdentity(payload map[string]interface{}, readerID string) {
	var reader models.ReaderModel
	if err := s.db.First(&reader, "id = ?", readerID).Error; err != nil {
		return
	}
	if reader.IsOwner {
		var owner models.UserModel
		if err := s.db.Select("name, avatar").First(&owner).Error; err == nil {
			if name := strings.TrimSpace(owner.Name); name != "" {
				payload["author"] = name
			}
			if avatar := strings.TrimSpace(owner.Avatar); avatar != "" {
				payload["avatar"] = avatar
			}
			return
		}
	}
	if name := strings.TrimSpace(reader.Name); name != "" {
		payload["author"] = name
	}
	if image := strings.TrimSpace(reader.Image); image != "" {
		payload["avatar"] = image
	}
}

func (s *Service) normalizeActivityLikePayload(payload interface{}) interface{} {
	raw := payloadAsMap(payload)
	if len(raw) == 0 {
		return payload
	}

	ref := payloadAsMap(raw["ref"])
	refPayload := map[string]interface{}{}
	if id := stringFromMap(ref, "id"); id != "" {
		refPayload["id"] = id
	}
	if title := stringFromMap(ref, "title"); title != "" {
		refPayload["title"] = title
	}
	if readerID := stringFromMap(ref, "readerId"); readerID != "" {
		refPayload["readerId"] = readerID
	}
	if len(refPayload) == 0 {
		id := stringFromMap(raw, "id")
		typeName := strings.ToLower(stringFromMap(raw, "type"))
		refPayload = s.buildActivityLikeRef(id, typeName)
	}

	normalized := map[string]interface{}{
		"id":      stringFromMap(raw, "id"),
		"type":    normalizeActivityLikeType(stringFromMap(raw, "type")),
		"created": raw["created"],
		"ref":     refPayload,
	}
	if reader := s.normalizeReaderPayload(raw); len(reader) > 0 {
		normalized["reader"] = reader
	}
	return normalized
}

func (s *Service) buildActivityLikeRef(id, typeName string) map[string]interface{} {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "post", "posts":
		var post models.PostModel
		if err := s.db.First(&post, "id = ?", id).Error; err == nil {
			return map[string]interface{}{"id": post.ID, "title": post.Title}
		}
	case "note", "notes":
		var note models.NoteModel
		if err := s.db.First(&note, "id = ?", id).Error; err == nil {
			return map[string]interface{}{"id": note.ID, "title": note.Title}
		}
	}
	return map[string]interface{}{}
}

func normalizeActivityLikeType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "note", "notes":
		return "Note"
	default:
		return "Post"
	}
}

func (s *Service) normalizeReaderPayload(raw map[string]interface{}) map[string]interface{} {
	if reader := payloadAsMap(raw["reader"]); len(reader) > 0 {
		return buildWebhookReaderPayload(reader)
	}
	if readerID := stringFromMap(raw, "readerId"); readerID != "" {
		if reader, err := s.loadReader(readerID); err == nil && reader != nil {
			return buildWebhookReaderModelPayload(reader)
		}
	}
	return nil
}

func extractPayloadID(payload interface{}) string {
	raw := payloadAsMap(payload)
	if len(raw) == 0 {
		if v, ok := payload.(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	if id := stringFromMap(raw, "id"); id != "" {
		return id
	}
	if id := stringFromMap(raw, "data"); id != "" {
		return id
	}
	return ""
}

func payloadAsMap(payload interface{}) map[string]interface{} {
	if payload == nil {
		return nil
	}
	if m, ok := payload.(map[string]interface{}); ok {
		return m
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil
	}
	return out
}

func stringFromMap(m map[string]interface{}, key string) string {
	if len(m) == 0 {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func collectionRefType(refType models.RefType) string {
	switch refType {
	case models.RefTypePost:
		return "posts"
	case models.RefTypeNote:
		return "notes"
	case models.RefTypePage:
		return "pages"
	case models.RefTypeRecently:
		return "recentlies"
	default:
		return strings.TrimSpace(string(refType))
	}
}

func normalizeWebhookRecentlyType(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return "text"
	}
	return v
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func nullableString(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func mustJSONString(value interface{}) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

func buildWebhookReaderModelPayload(reader *models.ReaderModel) map[string]interface{} {
	if reader == nil {
		return nil
	}
	role := "reader"
	if reader.IsOwner {
		role = "owner"
	}
	return map[string]interface{}{
		"id":      reader.ID,
		"created": reader.CreatedAt,
		"email":   reader.Email,
		"name":    reader.Name,
		"handle":  reader.Handle,
		"image":   reader.Image,
		"role":    role,
	}
}

func buildWebhookReaderPayload(raw map[string]interface{}) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	role := stringFromMap(raw, "role")
	if role == "" {
		if isOwner, ok := raw["isOwner"].(bool); ok && isOwner {
			role = "owner"
		} else {
			role = "reader"
		}
	}
	payload := map[string]interface{}{
		"role": role,
	}
	if id := stringFromMap(raw, "id"); id != "" {
		payload["id"] = id
	}
	if created, ok := raw["created"]; ok {
		payload["created"] = created
	}
	if email := stringFromMap(raw, "email"); email != "" {
		payload["email"] = email
	}
	if name := stringFromMap(raw, "name"); name != "" {
		payload["name"] = name
	}
	if handle := stringFromMap(raw, "handle"); handle != "" {
		payload["handle"] = handle
	}
	if image := stringFromMap(raw, "image"); image != "" {
		payload["image"] = image
	}
	return payload
}

var _ = gorm.ErrRecordNotFound
