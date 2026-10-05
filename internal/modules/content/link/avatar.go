package link

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/storage/file"
	"go.uber.org/zap"
)

const maxAvatarBytes = 2 << 20

var avatarClient = &http.Client{Timeout: 10 * time.Second}

var avatarExtensions = map[string]string{
	"image/png":                ".png",
	"image/jpeg":               ".jpg",
	"image/gif":                ".gif",
	"image/webp":               ".webp",
	"image/avif":               ".avif",
	"image/svg+xml":            ".svg",
	"image/x-icon":             ".ico",
	"image/vnd.microsoft.icon": ".ico",
}

// avatarInternalizationEnabled reports friendLinkOptions.enableAvatarInternalization and the API base for local URLs.
func (h *Handler) avatarInternalizationEnabled() (string, bool) {
	if h.cfgSvc == nil {
		return "", false
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.FriendLinkOptions.EnableAvatarInternalization {
		return "", false
	}
	base := cfg.URL.APIBaseURL()
	return base, base != ""
}

// internalizeAvatar copies an external avatar into the local "avatar" files and points the link at it.
// Returns true when the link was updated.
func (h *Handler) internalizeAvatar(l *models.LinkModel, apiBase string) bool {
	src := strings.TrimSpace(l.Avatar)
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		return false
	}
	if strings.HasPrefix(src, apiBase+"/") {
		return false
	}
	name, err := downloadAvatar(src)
	if err != nil {
		h.svc.logger.Warn("internalize link avatar failed", zap.String("link", l.ID), zap.String("avatar", src), zap.Error(err))
		return false
	}
	local := apiBase + "/objects/avatar/" + name
	if err := h.svc.db.Model(&models.LinkModel{}).Where("id = ?", l.ID).Update("avatar", local).Error; err != nil {
		return false
	}
	l.Avatar = local
	return true
}

func downloadAvatar(src string) (string, error) {
	resp, err := avatarClient.Get(src)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("not an image: %q", contentType)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxAvatarBytes {
		return "", fmt.Errorf("avatar is larger than %d bytes", maxAvatarBytes)
	}

	ext, ok := avatarExtensions[contentType]
	if !ok {
		ext = ".png"
	}
	// Same source, same file: re-running the migration does not pile up copies.
	sum := sha1.Sum([]byte(src))
	name := hex.EncodeToString(sum[:])[:20] + ext

	dir := filepath.Join(file.StaticDir(), "avatar")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// internalizeAvatarAsync runs after a link is created, approved or edited.
func (h *Handler) internalizeAvatarAsync(l *models.LinkModel) {
	if l == nil || l.State != models.LinkPass {
		return
	}
	apiBase, ok := h.avatarInternalizationEnabled()
	if !ok {
		return
	}
	link := *l
	go func() {
		if h.internalizeAvatar(&link, apiBase) {
			h.dispatchContentRefresh(link.ID)
		}
	}()
}
