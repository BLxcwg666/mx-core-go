package backup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func formatSize(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func renderBackupObjectKey(template, filename string, now time.Time) string {
	tpl := strings.TrimSpace(template)
	if tpl == "" {
		tpl = defaultS3PathTemplate
	}

	replacer := strings.NewReplacer(
		"{Y}", now.Format("2006"),
		"{m}", now.Format("01"),
		"{d}", now.Format("02"),
		"{H}", now.Format("15"),
		"{M}", now.Format("04"),
		"{s}", now.Format("05"),
		"{filename}", filename,
	)

	key := replacer.Replace(tpl)
	key = strings.ReplaceAll(key, "\\", "/")
	key = strings.TrimSpace(strings.TrimPrefix(key, "/"))
	for strings.Contains(key, "//") {
		key = strings.ReplaceAll(key, "//", "/")
	}
	if key == "" {
		return filename
	}
	return key
}

func camelToSnake(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	runes := []rune(raw)
	if len(runes) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(runes) + 4)

	lastUnderscore := false
	for i, r := range runes {
		if r == '-' || r == ' ' {
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		if r == '_' {
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}

		if unicode.IsUpper(r) {
			if i > 0 && !lastUnderscore {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || nextLower {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
			continue
		}

		b.WriteRune(unicode.ToLower(r))
		lastUnderscore = false
	}

	out := strings.Trim(b.String(), "_")
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	return out
}

func unixNumberToTime(value float64) (time.Time, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return time.Time{}, false
	}
	abs := math.Abs(value)
	switch {
	case abs >= 1e11:
		return time.UnixMilli(int64(value)), true
	case abs >= 1e8:
		return time.Unix(int64(value), 0), true
	default:
		return time.Time{}, false
	}
}

func parseTimeString(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := [...]string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func isJSONLikeType(dbType string) bool {
	dbType = strings.ToUpper(strings.TrimSpace(dbType))
	return strings.Contains(dbType, "JSON")
}

func isTextLikeType(dbType string) bool {
	dbType = strings.ToUpper(strings.TrimSpace(dbType))
	return strings.Contains(dbType, "CHAR") ||
		strings.Contains(dbType, "TEXT") ||
		strings.Contains(dbType, "CLOB") ||
		strings.Contains(dbType, "ENUM") ||
		strings.Contains(dbType, "SET")
}

func isTimeLikeType(dbType string) bool {
	dbType = strings.ToUpper(strings.TrimSpace(dbType))
	return strings.Contains(dbType, "TIME") ||
		strings.Contains(dbType, "DATE") ||
		strings.Contains(dbType, "YEAR")
}

func isDuplicateConstraintError(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint")
}

func normalizeBSONValue(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case primitive.Null:
		return nil
	case primitive.Undefined:
		return nil
	case primitive.ObjectID:
		return v.Hex()
	case primitive.DateTime:
		return v.Time()
	case primitive.Timestamp:
		return time.Unix(int64(v.T), 0).UTC()
	case primitive.Decimal128:
		return v.String()
	case primitive.Regex:
		return v.Pattern
	case primitive.JavaScript:
		return string(v)
	case primitive.Binary:
		return string(v.Data)
	case primitive.M:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			out[key] = normalizeBSONValue(item)
		}
		return out
	case primitive.D:
		out := make(map[string]interface{}, len(v))
		for _, item := range v {
			out[item.Key] = normalizeBSONValue(item.Value)
		}
		return out
	case primitive.A:
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeBSONValue(item))
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			out[key] = normalizeBSONValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeBSONValue(item))
		}
		return out
	case []byte:
		return string(v)
	default:
		return value
	}
}

func normalizeBackupValue(value interface{}) interface{} {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			out[key] = normalizeBackupValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeBackupValue(item))
		}
		return out
	default:
		return value
	}
}

func encodeBSONRows(rows []map[string]interface{}) ([]byte, error) {
	if len(rows) == 0 {
		return []byte{}, nil
	}
	buffer := bytes.NewBuffer(nil)
	for _, row := range rows {
		doc := make(map[string]interface{}, len(row))
		for key, value := range row {
			doc[key] = normalizeBackupValue(value)
		}
		b, err := bson.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if _, err := buffer.Write(b); err != nil {
			return nil, err
		}
	}
	return buffer.Bytes(), nil
}

func decodeBSONRows(payload []byte) ([]map[string]interface{}, error) {
	if len(payload) == 0 {
		return []map[string]interface{}{}, nil
	}
	rows := make([]map[string]interface{}, 0)
	cursor := 0
	for cursor < len(payload) {
		if cursor+4 > len(payload) {
			return nil, fmt.Errorf("invalid bson payload")
		}
		docLen := int(int32(binary.LittleEndian.Uint32(payload[cursor : cursor+4])))
		if docLen <= 0 || cursor+docLen > len(payload) {
			return nil, fmt.Errorf("invalid bson document length")
		}
		var row map[string]interface{}
		if err := bson.Unmarshal(payload[cursor:cursor+docLen], &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
		cursor += docLen
	}
	return rows, nil
}
