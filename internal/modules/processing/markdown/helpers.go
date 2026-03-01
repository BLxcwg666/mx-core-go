package markdown

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// parseBool converts common truthy query-string values to bool.
func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// chooseFirstNonEmpty returns primary when non-blank, otherwise fallback.
func chooseFirstNonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

// markdownFilename builds the .md filename for an exported article.
func markdownFilename(meta map[string]any, useSlug bool) string {
	filename := asString(meta["title"])
	if useSlug {
		filename = asString(meta["slug"])
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "untitled"
	}
	filename = strings.ReplaceAll(filename, "/", "-")
	filename = strings.ReplaceAll(filename, "\\", "-")
	return filename + ".md"
}

// asString converts arbitrary values to their string representation.
func asString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// parseMetaDates resolves created/updated timestamps from import metadata.
func parseMetaDates(meta *importMeta) (time.Time, time.Time) {
	now := time.Now()
	if meta == nil {
		return now, now
	}

	created := parseTime(meta.Date)
	if created.IsZero() {
		created = now
	}

	updated := parseTime(meta.Updated)
	if updated.IsZero() {
		updated = created
	}
	return created, updated
}

// parseTime attempts several common date/time layouts.
func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// exportMetaWithoutText marshals v and strips the "text" and "__v" keys.
func exportMetaWithoutText(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}

	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	delete(out, "text")
	delete(out, "__v")
	return out
}

// markdownBuilder assembles the full markdown document for a single article.
func markdownBuilder(meta map[string]any, text string, includeYAMLHeader, showHeader bool) string {
	title := asString(meta["title"])
	var sb strings.Builder
	if includeYAMLHeader {
		header := map[string]any{
			"date":    meta["created"],
			"updated": meta["modified"],
			"title":   title,
		}
		for key, value := range meta {
			if key == "created" || key == "modified" || key == "title" {
				continue
			}
			header[key] = value
		}
		yamlText, _ := yaml.Marshal(header)
		sb.WriteString("---\n")
		sb.WriteString(strings.TrimSpace(string(yamlText)))
		sb.WriteString("\n---\n\n")
	}
	if showHeader {
		sb.WriteString("# ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
	}
	sb.WriteString(strings.TrimSpace(text))
	return sb.String()
}

// toSlashPath converts a filepath to forward-slash form for use inside zips.
func toSlashPath(parts ...string) string {
	return filepath.ToSlash(filepath.Join(parts...))
}
