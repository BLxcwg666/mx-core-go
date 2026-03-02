package activity

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway/gateway"
	"github.com/mx-space/core/internal/pkg/pagination"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type Handler struct {
	db  *gorm.DB
	hub *gateway.Hub
}

func NewHandler(db *gorm.DB, hub *gateway.Hub) *Handler { return &Handler{db: db, hub: hub} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/activity")

	g.POST("/like", h.like)
	g.GET("/likes", authMW, h.listLikes)
	g.GET("", authMW, h.list)

	g.POST("/presence/update", h.updatePresence)
	g.GET("/presence", h.getPresence)

	g.GET("/rooms", h.getRooms)
	g.GET("/online-count", h.getOnlineCount)

	g.GET("/recent", h.getRecent)
	g.GET("/recent/notification", h.getRecentNotification)
	g.GET("/last-year/publication", h.getLastYearPublication)

	g.GET("/reading/rank", authMW, h.getReadingRank)

	g.DELETE("/all", authMW, h.deleteAll)
	g.DELETE("/:type", authMW, h.deleteByType)
}

func (h *Handler) like(c *gin.Context) {
	var dto likeDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	contentType := strings.ToLower(dto.Type)
	var tx *gorm.DB
	switch contentType {
	case "post", "posts":
		tx = h.db.Model(&models.PostModel{}).Where("id = ?", dto.ID).
			UpdateColumn("like_count", gorm.Expr("like_count + 1"))
		contentType = "post"
	case "note", "notes":
		tx = h.db.Model(&models.NoteModel{}).Where("id = ?", dto.ID).
			UpdateColumn("like_count", gorm.Expr("like_count + 1"))
		contentType = "note"
	default:
		response.BadRequest(c, "type must be post|note")
		return
	}
	if tx.Error != nil {
		response.InternalError(c, tx.Error)
		return
	}
	if tx.RowsAffected == 0 {
		response.NotFoundMsg(c, "内容不存在")
		return
	}

	act := models.ActivityModel{
		Type: fmt.Sprintf("%d", activityTypeLike),
		Payload: map[string]interface{}{
			"id":   dto.ID,
			"type": contentType,
			"ip":   c.ClientIP(),
		},
	}
	if err := h.db.Create(&act).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) listLikes(c *gin.Context) {
	h.listLikePaged(c)
}

func (h *Handler) list(c *gin.Context) {
	switch c.DefaultQuery("type", "0") {
	case "0", "like":
		h.listLikePaged(c)
	case "1", "read_duration":
		h.listReadDurationPaged(c)
	default:
		response.BadRequest(c, "type must be 0|1")
	}
}

func (h *Handler) listLikePaged(c *gin.Context) {
	q := pagination.FromContext(c)
	tx := h.db.Model(&models.ActivityModel{}).
		Where("type = ?", fmt.Sprintf("%d", activityTypeLike)).
		Order("created_at DESC")

	var rows []models.ActivityModel
	pag, err := pagination.Paginate(tx, q, &rows)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	postIDs := make([]string, 0)
	noteIDs := make([]string, 0)
	for _, row := range rows {
		id := strFromAny(row.Payload["id"])
		if id == "" {
			continue
		}
		switch strings.ToLower(strFromAny(row.Payload["type"])) {
		case "post", "posts":
			postIDs = append(postIDs, id)
		case "note", "notes":
			noteIDs = append(noteIDs, id)
		}
	}

	postMap := map[string]models.PostModel{}
	noteMap := map[string]models.NoteModel{}

	if len(postIDs) > 0 {
		var posts []models.PostModel
		if err := h.db.Preload("Category").Where("id IN ?", uniq(postIDs)).Find(&posts).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		for _, p := range posts {
			postMap[p.ID] = p
		}
	}

	if len(noteIDs) > 0 {
		var notes []models.NoteModel
		if err := h.db.Where("id IN ?", uniq(noteIDs)).Find(&notes).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		for _, n := range notes {
			noteMap[n.ID] = n
		}
	}

	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		payload := copyPayload(row.Payload)
		id := strFromAny(payload["id"])
		item := gin.H{
			"id":      row.ID,
			"type":    activityTypeLike,
			"payload": payload,
			"created": row.CreatedAt,
		}

		switch strings.ToLower(strFromAny(payload["type"])) {
		case "post", "posts":
			if post, ok := postMap[id]; ok {
				item["ref"] = compactPost(post)
			}
		case "note", "notes":
			if note, ok := noteMap[id]; ok {
				item["ref"] = compactNote(note)
			}
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       items,
		"pagination": pag,
	})
}

func (h *Handler) listReadDurationPaged(c *gin.Context) {
	q := pagination.FromContext(c)
	tx := h.db.Model(&models.ActivityModel{}).
		Where("type = ?", fmt.Sprintf("%d", activityTypeReadDuration)).
		Order("created_at DESC")

	var rows []models.ActivityModel
	pag, err := pagination.Paginate(tx, q, &rows)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	refIDs := make([]string, 0, len(rows))
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		payload := copyPayload(row.Payload)
		roomName := firstNonEmpty(strFromAny(payload["roomName"]), strFromAny(payload["room_name"]))
		refID := extractRefIDFromRoomName(roomName)
		if refID != "" {
			refIDs = append(refIDs, refID)
		}
		items = append(items, gin.H{
			"id":      row.ID,
			"type":    activityTypeReadDuration,
			"payload": payload,
			"refId":   refID,
			"created": row.CreatedAt,
		})
	}

	objects, _, err := h.loadObjectsByIDs(uniq(refIDs))
	if err != nil {
		response.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       items,
		"pagination": pag,
		"objects":    objects,
	})
}

func (h *Handler) updatePresence(c *gin.Context) {
	var dto presenceUpdateDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	now := nowMillis()
	if dto.TS == 0 {
		dto.TS = now
	}
	if h.hub != nil {
		if !h.hub.SIDInPublicRoom(dto.SID, dto.RoomName) {
			response.NoContent(c)
			return
		}
		h.hub.SetSIDIdentity(dto.SID, dto.Identity)
	}

	entry := upsertPresence(dto, c.ClientIP())

	row := models.ActivityModel{
		Type: fmt.Sprintf("%d", activityTypeReadDuration),
		Payload: map[string]interface{}{
			"identity":      entry.Identity,
			"position":      entry.Position,
			"roomName":      entry.RoomName,
			"sid":           entry.SID,
			"displayName":   entry.DisplayName,
			"readerId":      entry.ReaderID,
			"connectedAt":   entry.ConnectedAt,
			"joinedAt":      entry.JoinedAt,
			"updatedAt":     entry.UpdatedAt,
			"operationTime": entry.OperationTime,
			"ip":            entry.IP,
		},
	}
	_ = h.db.Create(&row).Error

	sanitized := sanitizePresence(entry)
	if dto.ReaderID != "" {
		readers, err := h.loadReadersByIDs([]string{dto.ReaderID})
		if err == nil {
			if reader, ok := readers[dto.ReaderID]; ok {
				sanitized["reader"] = reader
			}
		}
	}
	if h.hub != nil {
		h.hub.BroadcastPublic("ACTIVITY_UPDATE_PRESENCE", sanitized)
	}
	response.OK(c, sanitized)
}

func (h *Handler) getPresence(c *gin.Context) {
	var query getPresenceQuery
	_ = c.ShouldBindQuery(&query)
	if query.RoomName == "" {
		query.RoomName = c.Query("roomName")
	}
	if query.RoomName == "" {
		response.BadRequest(c, "room_name is required")
		return
	}

	if h.hub != nil {
		prunePresenceBySocketState(h.hub.HasSID, h.hub.SIDInPublicRoom)
	}

	entries := listRoomPresence(query.RoomName)
	data := map[string]gin.H{}
	readerIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		data[e.Identity] = sanitizePresence(e)
		if e.ReaderID != "" {
			readerIDs = append(readerIDs, e.ReaderID)
		}
	}

	readers := gin.H{}
	if len(readerIDs) > 0 {
		readerMap, err := h.loadReadersByIDs(readerIDs)
		if err != nil {
			response.InternalError(c, err)
			return
		}
		readers = readerMap
	}
	response.OK(c, gin.H{
		"data":    data,
		"readers": readers,
	})
}

func (h *Handler) getRooms(c *gin.Context) {
	var (
		roomNames []string
		roomCount map[string]int
	)
	if h.hub != nil {
		roomCount = h.hub.PublicRoomCount()
		roomNames = sortedRoomNames(roomCount)
	} else {
		roomNames, roomCount = getAllRooms()
	}
	roomCount = normalizeRoomCount(roomCount)

	ids := make([]string, 0, len(roomNames))
	for _, room := range roomNames {
		if id := extractRefIDFromRoomName(room); id != "" {
			ids = append(ids, id)
		}
	}
	objects, _, err := h.loadObjectsByIDs(uniq(ids))
	if err != nil {
		response.InternalError(c, err)
		return
	}

	response.OK(c, gin.H{
		"rooms":     roomNames,
		"roomCount": roomCount,
		"objects": gin.H{
			"posts": objects["posts"],
			"notes": objects["notes"],
			"pages": objects["pages"],
		},
	})
}

func normalizeRoomCount(roomCount map[string]int) map[string]int {
	out := make(map[string]int, len(roomCount)*3)
	for roomName, count := range roomCount {
		aliases := roomNameAliases(roomName)
		for _, alias := range aliases {
			out[alias] += count
		}
	}
	return out
}

func (h *Handler) loadReadersByIDs(ids []string) (gin.H, error) {
	ids = uniq(ids)
	if len(ids) == 0 {
		return gin.H{}, nil
	}

	var readers []models.ReaderModel
	if err := h.db.Where("id IN ?", ids).Find(&readers).Error; err != nil {
		return nil, err
	}

	out := make(gin.H, len(readers))
	for _, reader := range readers {
		out[reader.ID] = gin.H{
			"id":       reader.ID,
			"email":    reader.Email,
			"isOwner":  reader.IsOwner,
			"image":    reader.Image,
			"name":     reader.Name,
			"provider": "",
			"handle":   reader.Handle,
		}
	}
	return out, nil
}

func (h *Handler) getOnlineCount(c *gin.Context) {
	roomCount := map[string]int{}
	if h.hub != nil {
		roomCount = h.hub.PublicRoomCount()
	} else {
		_, roomCount = getAllRooms()
	}
	total := 0
	for _, count := range roomCount {
		total += count
	}
	response.OK(c, gin.H{
		"total": total,
		"rooms": roomCount,
	})
}

func sortedRoomNames(roomCount map[string]int) []string {
	rooms := make([]string, 0, len(roomCount))
	for roomName := range roomCount {
		rooms = append(rooms, roomName)
	}
	sort.Strings(rooms)
	return rooms
}

func (h *Handler) getReadingRank(c *gin.Context) {
	startAt := parseMsOrDefault(c.Query("start"), time.Now().AddDate(0, -6, 0))
	endAt := parseMsOrDefault(c.Query("end"), time.Now())

	var rows []models.ActivityModel
	if err := h.db.Where("type = ? AND created_at >= ? AND created_at <= ?",
		fmt.Sprintf("%d", activityTypeReadDuration), startAt, endAt).
		Order("created_at DESC").Find(&rows).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	counter := map[string]int{}
	for _, row := range rows {
		roomName := firstNonEmpty(strFromAny(row.Payload["roomName"]), strFromAny(row.Payload["room_name"]))
		refID := extractRefIDFromRoomName(roomName)
		if refID == "" {
			continue
		}
		counter[refID]++
	}

	ids := make([]string, 0, len(counter))
	for id := range counter {
		ids = append(ids, id)
	}
	_, flat, err := h.loadObjectsByIDs(ids)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	items := make([]gin.H, 0, len(counter))
	for id, count := range counter {
		items = append(items, gin.H{
			"refId": id,
			"count": count,
			"ref":   flat[id],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["count"].(int) > items[j]["count"].(int)
	})

	response.OK(c, gin.H{"data": items})
}

func (h *Handler) deleteByType(c *gin.Context) {
	typeStr := c.Param("type")
	before := time.Now()
	var body struct {
		Before int64 `json:"before"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.Before > 0 {
		before = time.UnixMilli(body.Before)
	}

	if _, err := strconv.Atoi(typeStr); err != nil {
		response.BadRequest(c, "type must be number")
		return
	}

	if err := h.db.Where("type = ? AND created_at < ?", typeStr, before).
		Delete(&models.ActivityModel{}).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) deleteAll(c *gin.Context) {
	if err := h.db.Session(&gorm.Session{AllowGlobalUpdate: true}).
		Delete(&models.ActivityModel{}).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) getRecent(c *gin.Context) {
	like, err := h.getRecentLike(5)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	comment, err := h.getRecentComment(3)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	recent, post, note, err := h.getRecentPublish(3)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	response.OK(c, gin.H{
		"like":    like,
		"comment": comment,
		"recent":  recent,
		"post":    post,
		"note":    note,
	})
}

func (h *Handler) getRecentNotification(c *gin.Context) {
	fromDate, err := time.Parse(time.RFC3339, c.Query("from"))
	if err != nil {
		if ms, convErr := strconv.ParseInt(c.Query("from"), 10, 64); convErr == nil {
			fromDate = time.UnixMilli(ms)
		} else {
			response.OK(c, []interface{}{})
			return
		}
	}
	if fromDate.After(time.Now()) {
		response.OK(c, []interface{}{})
		return
	}

	_, post, note, err := h.getRecentPublish(50)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	out := make([]gin.H, 0)
	for _, p := range post {
		created, _ := p["created"].(time.Time)
		if created.After(fromDate) {
			out = append(out, gin.H{
				"title": p["title"],
				"type":  "posts",
				"id":    p["id"],
				"slug":  p["slug"],
			})
		}
	}
	for _, n := range note {
		created, _ := n["created"].(time.Time)
		if created.After(fromDate) {
			out = append(out, gin.H{
				"title": n["title"],
				"type":  "notes",
				"id":    n["nid"],
			})
		}
	}
	response.OK(c, out)
}

func (h *Handler) getLastYearPublication(c *gin.Context) {
	start := time.Now().AddDate(-1, 0, 0)

	var posts []models.PostModel
	if err := h.db.Preload("Category").
		Where("created_at >= ?", start).
		Order("created_at DESC").
		Find(&posts).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	var notes []models.NoteModel
	if err := h.db.Where("created_at >= ?", start).
		Order("created_at DESC").Find(&notes).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	postOut := make([]gin.H, 0, len(posts))
	for _, p := range posts {
		postOut = append(postOut, compactPost(p))
	}

	noteOut := make([]gin.H, 0, len(notes))
	for _, n := range notes {
		title := n.Title
		if n.Password != "" || !n.IsPublished {
			title = "未公开的日记"
		}
		noteOut = append(noteOut, gin.H{
			"id":       n.ID,
			"title":    title,
			"created":  n.CreatedAt,
			"nid":      n.NID,
			"mood":     n.Mood,
			"weather":  n.Weather,
			"bookmark": n.Bookmark,
		})
	}

	response.OK(c, gin.H{
		"posts": postOut,
		"notes": noteOut,
	})
}

func (h *Handler) getRecentLike(limit int) ([]gin.H, error) {
	var rows []models.ActivityModel
	if err := h.db.Where("type = ?", fmt.Sprintf("%d", activityTypeLike)).
		Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		id := strFromAny(row.Payload["id"])
		if id != "" {
			ids = append(ids, id)
		}
	}
	_, flat, err := h.loadObjectsByIDs(uniq(ids))
	if err != nil {
		return nil, err
	}

	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		id := strFromAny(row.Payload["id"])
		ref := flat[id]
		if ref == nil {
			out = append(out, gin.H{
				"id":      row.ID,
				"title":   "已删除的内容",
				"created": row.CreatedAt,
			})
			continue
		}

		item := gin.H{
			"id":      row.ID,
			"title":   ref["title"],
			"created": row.CreatedAt,
		}
		switch strings.ToLower(strFromAny(row.Payload["type"])) {
		case "note", "notes":
			item["type"] = "notes"
			item["nid"] = ref["nid"]
		default:
			item["type"] = "posts"
			item["slug"] = ref["slug"]
		}
		out = append(out, item)
	}
	return out, nil
}

func (h *Handler) getRecentComment(limit int) ([]gin.H, error) {
	var comments []models.CommentModel
	if err := h.db.Where("is_whispers = ?", false).
		Order("created_at DESC").
		Limit(limit).
		Find(&comments).Error; err != nil {
		return nil, err
	}

	out := make([]gin.H, 0, len(comments))
	for _, cm := range comments {
		row := gin.H{
			"created": cm.CreatedAt,
			"author":  cm.Author,
			"text":    cm.Text,
			"avatar":  cm.Avatar,
		}
		switch cm.RefType {
		case models.RefTypePost:
			var post models.PostModel
			if err := h.db.Select("id, title, slug").First(&post, "id = ?", cm.RefID).Error; err == nil {
				row["id"] = post.ID
				row["title"] = post.Title
				row["slug"] = post.Slug
				row["type"] = "posts"
			}
		case models.RefTypeNote:
			var note models.NoteModel
			if err := h.db.Select("id, title, n_id").First(&note, "id = ?", cm.RefID).Error; err == nil {
				row["id"] = note.ID
				row["title"] = note.Title
				row["nid"] = note.NID
				row["type"] = "notes"
			}
		case models.RefTypePage:
			var page models.PageModel
			if err := h.db.Select("id, title, slug").First(&page, "id = ?", cm.RefID).Error; err == nil {
				row["id"] = page.ID
				row["title"] = page.Title
				row["slug"] = page.Slug
				row["type"] = "pages"
			}
		case models.RefTypeRecently:
			row["id"] = cm.RefID
			row["type"] = "recentlies"
		}
		out = append(out, row)
	}
	return out, nil
}

func (h *Handler) getRecentPublish(limit int) (recent []gin.H, post []gin.H, note []gin.H, err error) {
	var recents []models.RecentlyModel
	if err = h.db.Order("created_at DESC").Limit(limit).Find(&recents).Error; err != nil {
		return
	}
	recent = make([]gin.H, 0, len(recents))
	for _, r := range recents {
		recent = append(recent, gin.H{
			"id":      r.ID,
			"content": r.Content,
			"up":      r.UpCount,
			"down":    r.DownCount,
			"created": r.CreatedAt,
		})
	}

	var posts []models.PostModel
	if err = h.db.Preload("Category").Order("created_at DESC").Limit(limit).Find(&posts).Error; err != nil {
		return
	}
	post = make([]gin.H, 0, len(posts))
	for _, p := range posts {
		post = append(post, compactPost(p))
	}

	var notes []models.NoteModel
	if err = h.db.Where("is_published = ?", true).Order("created_at DESC").Limit(limit).Find(&notes).Error; err != nil {
		return
	}
	note = make([]gin.H, 0, len(notes))
	for _, n := range notes {
		note = append(note, compactNote(n))
	}
	return
}

func (h *Handler) loadObjectsByIDs(ids []string) (map[string][]gin.H, map[string]gin.H, error) {
	objects := map[string][]gin.H{
		"posts":      {},
		"notes":      {},
		"pages":      {},
		"recentlies": {},
	}
	flat := map[string]gin.H{}
	if len(ids) == 0 {
		return objects, flat, nil
	}

	ids = uniq(ids)

	var posts []models.PostModel
	if err := h.db.Preload("Category").Where("id IN ?", ids).Find(&posts).Error; err != nil {
		return nil, nil, err
	}
	for _, p := range posts {
		item := compactPost(p)
		objects["posts"] = append(objects["posts"], item)
		flat[p.ID] = item
	}

	var notes []models.NoteModel
	if err := h.db.Where("id IN ?", ids).Find(&notes).Error; err != nil {
		return nil, nil, err
	}
	for _, n := range notes {
		item := compactNote(n)
		objects["notes"] = append(objects["notes"], item)
		flat[n.ID] = item
	}

	var pages []models.PageModel
	if err := h.db.Where("id IN ?", ids).Find(&pages).Error; err != nil {
		return nil, nil, err
	}
	for _, p := range pages {
		item := compactPage(p)
		objects["pages"] = append(objects["pages"], item)
		flat[p.ID] = item
	}

	var recentlies []models.RecentlyModel
	if err := h.db.Where("id IN ?", ids).Find(&recentlies).Error; err != nil {
		return nil, nil, err
	}
	for _, r := range recentlies {
		item := compactRecently(r)
		objects["recentlies"] = append(objects["recentlies"], item)
		flat[r.ID] = item
	}

	return objects, flat, nil
}
