package imagesync

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/modules/storage/backup"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"gorm.io/gorm"
)

// localImagePattern matches local image URLs like /objects/image/... or /files/image/...
var localImagePattern = regexp.MustCompile(`(?:(/(?:objects|files)/image/[^\s"')\]]+))`)

// Service handles syncing local images to S3-compatible object storage.
type Service struct {
	db        *gorm.DB
	cfgSvc    *appconfigs.Service
	staticDir string
}

// NewService creates a new image sync service.
func NewService(db *gorm.DB, cfgSvc *appconfigs.Service) *Service {
	return &Service{
		db:        db,
		cfgSvc:    cfgSvc,
		staticDir: resolveStaticDir(),
	}
}

// SyncURLs uploads a list of local image URLs to S3 and returns a mapping
// of original URL to S3 URL (or error string).
func (s *Service) SyncURLs(urls []string) []SyncResult {
	cfg, err := s.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.ImageStorageOptions.Enable {
		results := make([]SyncResult, len(urls))
		for i, u := range urls {
			results[i] = SyncResult{OriginalURL: u, Error: "image storage not enabled"}
		}
		return results
	}

	uploader, err := s.buildUploader(cfg)
	if err != nil {
		results := make([]SyncResult, len(urls))
		for i, u := range urls {
			results[i] = SyncResult{OriginalURL: u, Error: err.Error()}
		}
		return results
	}

	results := make([]SyncResult, len(urls))
	for i, u := range urls {
		results[i] = s.syncSingleURL(cfg, uploader, u)
	}
	return results
}

// SyncResult holds the result for a single URL sync attempt.
type SyncResult struct {
	OriginalURL string `json:"originalUrl"`
	S3URL       string `json:"s3Url,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (s *Service) syncSingleURL(cfg *appcfg.FullConfig, uploader backup.S3Uploader, localURL string) SyncResult {
	localPath := s.localPathFromURL(localURL)
	if localPath == "" {
		return SyncResult{OriginalURL: localURL, Error: "cannot resolve local path"}
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		return SyncResult{OriginalURL: localURL, Error: fmt.Sprintf("read file: %s", err.Error())}
	}

	// Determine content type.
	contentType := http.DetectContentType(data)

	// Build S3 object key with prefix.
	prefix := strings.TrimRight(strings.TrimSpace(cfg.ImageStorageOptions.Prefix), "/")
	fileName := filepath.Base(localPath)
	objectKey := fileName
	if prefix != "" {
		objectKey = prefix + "/" + fileName
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	s3URL, err := uploader.Upload(ctx, objectKey, data, contentType)
	if err != nil {
		return SyncResult{OriginalURL: localURL, Error: fmt.Sprintf("upload: %s", err.Error())}
	}

	// Optionally delete local file after sync.
	if cfg.ImageStorageOptions.DeleteLocalAfterSync {
		_ = os.Remove(localPath)
	}

	return SyncResult{OriginalURL: localURL, S3URL: s3URL}
}

func (s *Service) buildUploader(cfg *appcfg.FullConfig) (backup.S3Uploader, error) {
	opts := appcfg.S3Options{
		Region: cfg.ImageStorageOptions.Region,
	}
	if cfg.ImageStorageOptions.Endpoint != nil {
		opts.Endpoint = *cfg.ImageStorageOptions.Endpoint
	}
	if cfg.ImageStorageOptions.SecretID != nil {
		opts.AccessKeyID = *cfg.ImageStorageOptions.SecretID
	}
	if cfg.ImageStorageOptions.SecretKey != nil {
		opts.SecretAccessKey = *cfg.ImageStorageOptions.SecretKey
	}
	if cfg.ImageStorageOptions.Bucket != nil {
		opts.Bucket = *cfg.ImageStorageOptions.Bucket
	}
	if cfg.ImageStorageOptions.CustomDomain != "" {
		opts.CustomDomain = cfg.ImageStorageOptions.CustomDomain
	}

	return backup.NewS3Uploader(opts)
}

func (s *Service) localPathFromURL(rawURL string) string {
	// Strip query/fragment.
	rawURL = strings.SplitN(rawURL, "?", 2)[0]
	rawURL = strings.SplitN(rawURL, "#", 2)[0]

	parts := strings.Split(strings.Trim(rawURL, "/"), "/")
	for i := 0; i < len(parts)-2; i++ {
		seg := strings.ToLower(parts[i])
		if seg == "objects" || seg == "files" {
			typ := parts[i+1]
			name := parts[i+2]
			return filepath.Join(s.staticDir, typ, name)
		}
	}
	return ""
}

// ExtractLocalImageURLs finds all local image URLs in a markdown text.
func ExtractLocalImageURLs(text string) []string {
	matches := localImagePattern.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, ok := seen[m]; !ok {
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	return out
}

func resolveStaticDir() string {
	dir := os.Getenv("MX_STATIC_DIR")
	if dir == "" {
		dir = "static"
	}
	return dir
}

// ReplaceMarkdownImageURLs replaces local image URLs in text with their S3 counterparts.
func ReplaceMarkdownImageURLs(text string, replacements map[string]string) string {
	for original, s3URL := range replacements {
		text = strings.ReplaceAll(text, original, s3URL)
	}
	return text
}

// SyncMarkdownImages extracts local images from text, uploads them to S3,
// and returns the text with URLs replaced.
func (s *Service) SyncMarkdownImages(text string) (string, error) {
	localURLs := ExtractLocalImageURLs(text)
	if len(localURLs) == 0 {
		return text, nil
	}

	results := s.SyncURLs(localURLs)
	replacements := make(map[string]string, len(results))
	for _, r := range results {
		if r.S3URL != "" {
			replacements[r.OriginalURL] = r.S3URL
		}
	}

	if len(replacements) > 0 {
		text = ReplaceMarkdownImageURLs(text, replacements)
	}
	return text, nil
}

// SyncFunc is a function type for syncing images, used by the notify service.
type SyncFunc func(contentID, contentType string) error

// SyncContentImages syncs images for a given content item (post or note).
func (s *Service) SyncContentImages(contentID, contentType string) error {
	cfg, err := s.cfgSvc.Get()
	if err != nil || cfg == nil || !cfg.ImageStorageOptions.Enable || !cfg.ImageStorageOptions.SyncOnPublish {
		return nil
	}

	switch contentType {
	case "post":
		return s.syncPostImages(contentID)
	case "note":
		return s.syncNoteImages(contentID)
	default:
		return nil
	}
}

func (s *Service) syncPostImages(id string) error {
	var text string
	if err := s.db.Raw("SELECT text FROM posts WHERE id = ?", id).Scan(&text).Error; err != nil {
		return err
	}
	newText, err := s.SyncMarkdownImages(text)
	if err != nil {
		return err
	}
	if newText != text {
		return s.db.Exec("UPDATE posts SET text = ? WHERE id = ?", newText, id).Error
	}
	return nil
}

func (s *Service) syncNoteImages(id string) error {
	var text string
	if err := s.db.Raw("SELECT text FROM notes WHERE id = ?", id).Scan(&text).Error; err != nil {
		return err
	}
	newText, err := s.SyncMarkdownImages(text)
	if err != nil {
		return err
	}
	if newText != text {
		return s.db.Exec("UPDATE notes SET text = ? WHERE id = ?", newText, id).Error
	}
	return nil
}
