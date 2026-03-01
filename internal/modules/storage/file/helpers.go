package file

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	appcfg "github.com/mx-space/core/internal/config"
)

const EnvStaticDir = "MX_STATIC_DIR"

// resolveStaticDir returns the absolute path to the static file directory,
// reading MX_STATIC_DIR from the environment or falling back to the default.
func resolveStaticDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvStaticDir)); dir != "" {
		return appcfg.ResolveRuntimePath(dir, "")
	}
	return appcfg.ResolveRuntimePath("", "static")
}

// buildFileName generates a collision-resistant filename that preserves the
// original extension.
func buildFileName(original string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(original)))
	if ext == "" || len(ext) > 10 {
		ext = ".dat"
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:18] + ext
}

var strTokenPattern = regexp.MustCompile(`\{str-(\d+)\}`)

// renderImageBedObjectKey expands a template string with date, hash, and random tokens.
func renderImageBedObjectKey(template, originalName string, payload []byte, now time.Time) string {
	tpl := strings.TrimSpace(template)
	if tpl == "" {
		tpl = "images/{Y}/{m}/{uuid}.{ext}"
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(originalName)), ".")
	if ext == "" {
		ext = "dat"
	}

	filename := strings.TrimSuffix(filepath.Base(strings.TrimSpace(originalName)), filepath.Ext(strings.TrimSpace(originalName)))
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "file"
	}

	sum := md5.Sum(payload)
	md5Hex := hex.EncodeToString(sum[:])
	uuidValue := strings.ReplaceAll(uuid.NewString(), "-", "")

	replacer := strings.NewReplacer(
		"{Y}", now.Format("2006"),
		"{y}", now.Format("06"),
		"{m}", now.Format("01"),
		"{d}", now.Format("02"),
		"{h}", now.Format("15"),
		"{i}", now.Format("04"),
		"{s}", now.Format("05"),
		"{timestamp}", strconv.FormatInt(now.Unix(), 10),
		"{uuid}", uuidValue,
		"{md5}", md5Hex,
		"{md5-16}", md5Hex[:16],
		"{filename}", filename,
		"{ext}", ext,
	)

	key := replacer.Replace(tpl)
	key = strTokenPattern.ReplaceAllStringFunc(key, func(token string) string {
		matches := strTokenPattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		n, err := strconv.Atoi(matches[1])
		if err != nil || n <= 0 {
			return token
		}
		if n > 128 {
			n = 128
		}
		return randomString(n)
	})

	key = strings.ReplaceAll(key, "\\", "/")
	key = strings.TrimSpace(strings.TrimPrefix(key, "/"))
	for strings.Contains(key, "//") {
		key = strings.ReplaceAll(key, "//", "/")
	}
	if key == "" {
		return fmt.Sprintf("images/%s/%s/%s.%s", now.Format("2006"), now.Format("01"), uuidValue, ext)
	}
	return key
}

// validateImageBedFile checks extension and size against the configured limits.
func validateImageBedFile(filename string, size int64, allowedFormats string, maxSizeMB int) error {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(filename))), ".")
	if ext == "" {
		return fmt.Errorf("image format is required")
	}
	if maxSizeMB > 0 && size > int64(maxSizeMB)*1024*1024 {
		return fmt.Errorf("image size exceeds %dMB", maxSizeMB)
	}

	allowSet := make(map[string]struct{})
	for _, item := range strings.Split(allowedFormats, ",") {
		item = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item)), ".")
		if item == "" {
			continue
		}
		allowSet[item] = struct{}{}
	}
	if len(allowSet) == 0 {
		return nil
	}
	if _, ok := allowSet[ext]; !ok {
		return fmt.Errorf("image format .%s is not allowed", ext)
	}
	return nil
}

// detectContentType sniffs the MIME type from the fallback header, extension,
// or raw payload bytes, in that priority order.
func detectContentType(filename string, payload []byte, fallback string) string {
	contentType := strings.TrimSpace(fallback)
	if contentType != "" {
		return contentType
	}
	if ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filename))); ext != "" {
		if guessed := mime.TypeByExtension(ext); guessed != "" {
			return guessed
		}
	}
	if len(payload) > 0 {
		return http.DetectContentType(payload)
	}
	return "application/octet-stream"
}

// randomString generates a cryptographically random alphanumeric string of
// length n, falling back to UUID concatenation on rand.Read failure.
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		fallback := strings.ReplaceAll(uuid.NewString(), "-", "")
		for len(fallback) < n {
			fallback += strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		return fallback[:n]
	}
	for i := range buf {
		buf[i] = letters[int(buf[i])%len(letters)]
	}
	return string(buf)
}

// isImageType reports whether the file type is an image bucket.
func isImageType(typ string) bool {
	return typ == "image" || typ == "photo"
}

// normalizeType lower-cases and validates raw as a safe path segment.
func normalizeType(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || !isSafeSegment(raw) {
		return ""
	}
	return raw
}

// normalizeTypeDefault calls normalizeType and falls back to def when empty.
func normalizeTypeDefault(raw, def string) string {
	typ := normalizeType(raw)
	if typ != "" {
		return typ
	}
	return normalizeType(def)
}

// safeName returns the base name of raw only when it passes isSafeSegment.
func safeName(raw string) string {
	name := filepath.Base(strings.TrimSpace(raw))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	if !isSafeSegment(name) {
		return ""
	}
	return name
}

// isSafeSegment returns true when s contains only alphanumerics, hyphens,
// underscores, or dots.
func isSafeSegment(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// detectRoot infers whether the request came in under /files/ or /objects/.
func detectRoot(path string) string {
	switch {
	case strings.Contains(path, "/files/"):
		return "files"
	case strings.Contains(path, "/objects/"):
		return "objects"
	default:
		return "objects"
	}
}
