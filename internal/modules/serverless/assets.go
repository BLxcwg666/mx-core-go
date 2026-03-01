package serverless

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (h *Handler) writeAsset(rawPath string, data interface{}, options interface{}) error {
	relativePath, err := safeAssetRelativePath(rawPath)
	if err != nil {
		return err
	}

	fullPath, err := resolveAssetPath(resolveServerlessUserAssetDir(), relativePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}

	payload, err := normalizeAssetWriteData(data, parseAssetEncoding(options))
	if err != nil {
		return err
	}

	flags, err := parseAssetWriteFlag(options)
	if err != nil {
		return err
	}

	fileMode := parseAssetWriteMode(options)
	file, err := os.OpenFile(fullPath, flags, fileMode)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(payload); err != nil {
		return err
	}

	return nil
}

func (h *Handler) readAsset(rawPath string, options interface{}) (interface{}, error) {
	relativePath, err := safeAssetRelativePath(rawPath)
	if err != nil {
		return nil, err
	}

	userAssetPath, err := resolveAssetPath(resolveServerlessUserAssetDir(), relativePath)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(userAssetPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		embedAssetPath, pathErr := resolveAssetPath(resolveServerlessEmbedAssetDir(), relativePath)
		if pathErr != nil {
			return nil, pathErr
		}
		raw, err = os.ReadFile(embedAssetPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}

			raw, err = h.fetchOnlineAsset(relativePath)
			if err != nil {
				return nil, err
			}
			if mkErr := os.MkdirAll(filepath.Dir(embedAssetPath), 0o755); mkErr == nil {
				_ = os.WriteFile(embedAssetPath, raw, 0o644)
			}
		}
	}

	encoding := parseAssetEncoding(options)
	if encoding == "" || encoding == "buffer" {
		return raw, nil
	}

	switch encoding {
	case "utf8", "utf-8", "ascii", "latin1", "binary", "utf16le", "utf-16le":
		return string(raw), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(raw), nil
	case "hex":
		return hex.EncodeToString(raw), nil
	default:
		return nil, fmt.Errorf("unsupported readAsset encoding %q", encoding)
	}
}

func (h *Handler) fetchOnlineAsset(relativePath string) ([]byte, error) {
	urlPath := strings.TrimPrefix(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	targetURL := serverlessOnlineAssetBaseURL + urlPath

	resp, err := h.httpClient.Get(targetURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("asset %q not found", relativePath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func parseAssetWriteFlag(options interface{}) (int, error) {
	flag := "w"
	if cfg, ok := options.(map[string]interface{}); ok {
		if raw := strings.TrimSpace(strings.ToLower(toString(cfg["flag"]))); raw != "" {
			flag = raw
		}
	}

	switch flag {
	case "w":
		return os.O_CREATE | os.O_WRONLY | os.O_TRUNC, nil
	case "w+":
		return os.O_CREATE | os.O_RDWR | os.O_TRUNC, nil
	case "wx", "xw":
		return os.O_CREATE | os.O_WRONLY | os.O_TRUNC | os.O_EXCL, nil
	case "wx+", "xw+":
		return os.O_CREATE | os.O_RDWR | os.O_TRUNC | os.O_EXCL, nil
	case "a":
		return os.O_CREATE | os.O_WRONLY | os.O_APPEND, nil
	case "a+":
		return os.O_CREATE | os.O_RDWR | os.O_APPEND, nil
	case "ax", "xa":
		return os.O_CREATE | os.O_WRONLY | os.O_APPEND | os.O_EXCL, nil
	case "ax+", "xa+":
		return os.O_CREATE | os.O_RDWR | os.O_APPEND | os.O_EXCL, nil
	default:
		return 0, fmt.Errorf("unsupported writeAsset flag %q", flag)
	}
}

func parseAssetWriteMode(options interface{}) os.FileMode {
	const defaultMode = 0o644
	cfg, ok := options.(map[string]interface{})
	if !ok {
		return defaultMode
	}

	modeRaw, exists := cfg["mode"]
	if !exists || modeRaw == nil {
		return defaultMode
	}

	switch v := modeRaw.(type) {
	case int:
		return os.FileMode(v)
	case int8:
		return os.FileMode(v)
	case int16:
		return os.FileMode(v)
	case int32:
		return os.FileMode(v)
	case int64:
		return os.FileMode(v)
	case uint:
		return os.FileMode(v)
	case uint8:
		return os.FileMode(v)
	case uint16:
		return os.FileMode(v)
	case uint32:
		return os.FileMode(v)
	case uint64:
		return os.FileMode(v)
	case float32:
		return os.FileMode(int64(v))
	case float64:
		return os.FileMode(int64(v))
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return defaultMode
		}
		parsed, err := strconv.ParseInt(trimmed, 0, 64)
		if err != nil {
			return defaultMode
		}
		return os.FileMode(parsed)
	default:
		return defaultMode
	}
}

func normalizeAssetWriteData(data interface{}, encoding string) ([]byte, error) {
	switch value := data.(type) {
	case nil:
		return []byte{}, nil
	case string:
		return encodeAssetString(value, encoding)
	case []byte:
		cloned := make([]byte, len(value))
		copy(cloned, value)
		return cloned, nil
	case []interface{}:
		if b, ok := tryConvertToByteSlice(value); ok {
			return b, nil
		}
	}

	if marshaled, err := json.Marshal(data); err == nil {
		return marshaled, nil
	}
	return []byte(fmt.Sprintf("%v", data)), nil
}

func tryConvertToByteSlice(arr []interface{}) ([]byte, bool) {
	if len(arr) == 0 {
		return []byte{}, true
	}
	out := make([]byte, len(arr))
	for i := range arr {
		n := toInt(arr[i])
		if n < 0 || n > 255 {
			return nil, false
		}
		out[i] = byte(n)
	}
	return out, true
}

func encodeAssetString(content, encoding string) ([]byte, error) {
	switch encoding {
	case "", "utf8", "utf-8", "ascii", "latin1", "binary", "buffer", "utf16le", "utf-16le":
		return []byte(content), nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	case "hex":
		decoded, err := hex.DecodeString(content)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported writeAsset encoding %q", encoding)
	}
}

func parseAssetEncoding(options interface{}) string {
	switch value := options.(type) {
	case nil:
		return ""
	case string:
		return normalizeAssetEncoding(value)
	case map[string]interface{}:
		return normalizeAssetEncoding(toString(value["encoding"]))
	default:
		return ""
	}
}

func normalizeAssetEncoding(raw string) string {
	return strings.TrimSpace(strings.ToLower(raw))
}

func safeAssetRelativePath(rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", errors.New("asset path is required")
	}

	sanitized := strings.ReplaceAll(rawPath, "\\", "/")
	parts := strings.Split(sanitized, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if isUnsafeAssetSegment(part) {
			part = "."
		}
		if part == "." {
			continue
		}
		cleaned = append(cleaned, part)
	}

	if len(cleaned) == 0 {
		return "", errors.New("asset path is required")
	}
	return filepath.Join(cleaned...), nil
}

func isUnsafeAssetSegment(segment string) bool {
	if segment == "~" {
		return true
	}
	if len(segment) < 2 {
		return false
	}
	for _, r := range segment {
		if r != '.' {
			return false
		}
	}
	return true
}

func resolveAssetPath(rootDir, relativePath string) (string, error) {
	root := filepath.Clean(rootDir)
	target := filepath.Clean(filepath.Join(root, relativePath))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("asset path escapes root directory")
	}
	return target, nil
}

func resolveServerlessUserAssetDir() string {
	if customDir := strings.TrimSpace(os.Getenv("MX_USER_ASSET_DIR")); customDir != "" {
		return filepath.Clean(customDir)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("NODE_ENV")), "development") {
		return filepath.Join(".", "tmp", "assets")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".mx-space", "assets")
	}
	return filepath.Join(".", "assets")
}

func resolveServerlessEmbedAssetDir() string {
	return filepath.Join(".", "assets")
}
