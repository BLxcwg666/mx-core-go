package file

import (
	"strings"

	"github.com/mx-space/core/internal/models"
)

// Places an uploaded file can be used from. Nothing marks a reference "active" when content is
// saved, so before treating "pending" uploads as orphans they are looked up here; without this
// the orphan cleanup deleted every uploaded image.
var fileUsageColumns = []struct{ table, column string }{
	{"posts", "text"}, {"posts", "meta"}, {"posts", "images"},
	{"notes", "text"}, {"notes", "meta"}, {"notes", "images"},
	{"pages", "text"}, {"pages", "meta"}, {"pages", "images"},
	{"drafts", "text"}, {"drafts", "meta"},
	{"recentlies", "content"},
	{"comments", "text"},
	{"says", "text"},
	{"users", "avatar"},
	{"topics", "icon"},
	{"links", "avatar"},
	{"projects", "avatar"}, {"projects", "images"}, {"projects", "text"},
}

var likeEscaper = strings.NewReplacer(`\`, `\`, `%`, `\%`, `_`, `\_`)

func (h *Handler) isFileReferenced(fileName string) bool {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return true
	}
	pattern := "%" + likeEscaper.Replace(fileName) + "%"
	for _, u := range fileUsageColumns {
		var count int64
		if err := h.db.Table(u.table).Where(u.column+" LIKE ?", pattern).Limit(1).Count(&count).Error; err != nil {
			// When in doubt keep the file.
			return true
		}
		if count > 0 {
			return true
		}
	}
	return false
}

// activateReferencedFiles marks pending uploads that are used somewhere as active.
func (h *Handler) activateReferencedFiles() {
	var refs []models.FileReferenceModel
	if err := h.db.Select("id, file_name").Where("status = ?", "pending").Find(&refs).Error; err != nil {
		return
	}
	for _, ref := range refs {
		if h.isFileReferenced(ref.FileName) {
			h.db.Model(&models.FileReferenceModel{}).Where("id = ?", ref.ID).Update("status", "active")
		}
	}
}
