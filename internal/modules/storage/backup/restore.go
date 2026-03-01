package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mx-space/core/internal/config"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"gorm.io/gorm"
)

// RestoreFromZip imports table dumps from a backup ZIP.
func RestoreFromZip(db *gorm.DB, zr *zip.Reader) error {
	if db == nil || zr == nil {
		return fmt.Errorf("invalid restore input")
	}

	tableEntries := make(map[string]backupEntryCandidate)
	for _, file := range zr.File {
		table, format, ok := parseBackupEntry(file.Name)
		if !ok {
			continue
		}

		table = resolveRestoreTableName(table)
		if table == "" {
			continue
		}

		exist, has := tableEntries[table]
		if !has || (exist.Format != "bson" && format == "bson") {
			tableEntries[table] = backupEntryCandidate{File: file, Format: format}
		}
	}

	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			_ = tx.Rollback().Error
		}
	}()

	fkCheckDisabled := false
	if strings.EqualFold(tx.Dialector.Name(), "mysql") {
		if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			return err
		}
		fkCheckDisabled = true
		defer func() {
			if fkCheckDisabled {
				_ = tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
			}
		}()
	}

	columnCache := make(map[string]map[string]tableColumn, len(tableEntries))
	for _, table := range backupTableNames {
		entry, ok := tableEntries[table]
		if !ok {
			continue
		}
		rows, err := decodeBackupRows(entry.File, entry.Format)
		if err != nil {
			return fmt.Errorf("decode backup rows for table %s failed: %w", table, err)
		}

		columns, hasColumns := columnCache[table]
		if !hasColumns {
			columns, err = loadTableColumns(tx, table)
			if err != nil {
				return fmt.Errorf("load table columns for %s failed: %w", table, err)
			}
			columnCache[table] = columns
		}

		normalizedRows := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			normalized := normalizeRestoreRow(table, row, columns)
			if len(normalized) == 0 {
				continue
			}
			normalizedRows = append(normalizedRows, normalized)
		}

		if err := tx.Exec("DELETE FROM `" + table + "`").Error; err != nil {
			return err
		}
		for idx, row := range normalizedRows {
			if err := tx.Table(table).Create(row).Error; err != nil {
				if isDuplicateConstraintError(err) {
					continue
				}
				return fmt.Errorf("insert row #%d into %s failed: %w", idx+1, table, err)
			}
		}
	}

	if fkCheckDisabled {
		if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error; err != nil {
			return err
		}
		fkCheckDisabled = false
	}
	if err := migrateLegacyOptions(tx); err != nil {
		return err
	}
	if err := importLegacyEmailTemplates(tx, zr); err != nil {
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	shouldRollback = false
	return nil
}

func parseBackupEntry(name string) (table string, format string, ok bool) {
	base := strings.ToLower(strings.TrimSpace(path.Base(name)))
	if base == "" {
		return "", "", false
	}
	if base == "prelude.json" || base == "manifest.json" || strings.HasSuffix(base, ".metadata.json") {
		return "", "", false
	}

	if strings.HasSuffix(base, ".bson") {
		table = strings.TrimSuffix(base, ".bson")
		if table == "" {
			return "", "", false
		}
		return table, "bson", true
	}
	if strings.HasSuffix(base, ".json") {
		table = strings.TrimSuffix(base, ".json")
		if table == "" {
			return "", "", false
		}
		return table, "json", true
	}
	return "", "", false
}

func resolveRestoreTableName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	if mapped, ok := restoreTableAliases[name]; ok {
		name = mapped
	}
	if _, ok := backupTableNameSet[name]; !ok {
		return ""
	}
	return name
}

func decodeBackupRows(file *zip.File, format string) ([]map[string]interface{}, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	switch format {
	case "bson":
		return decodeBSONRows(data)
	case "json":
		if len(bytes.TrimSpace(data)) == 0 {
			return []map[string]interface{}{}, nil
		}
		var rows []map[string]interface{}
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, err
		}
		return rows, nil
	default:
		return nil, fmt.Errorf("unsupported backup format: %s", format)
	}
}

func loadTableColumns(db *gorm.DB, table string) (map[string]tableColumn, error) {
	columnTypes, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return nil, err
	}
	result := make(map[string]tableColumn, len(columnTypes))
	for _, columnType := range columnTypes {
		name := strings.ToLower(strings.TrimSpace(columnType.Name()))
		if name == "" {
			continue
		}
		result[name] = tableColumn{
			DBType: strings.ToUpper(strings.TrimSpace(columnType.DatabaseTypeName())),
		}
	}
	return result, nil
}

func normalizeRestoreRow(table string, row map[string]interface{}, columns map[string]tableColumn) map[string]interface{} {
	if len(row) == 0 {
		return nil
	}
	result := make(map[string]interface{}, len(row))
	for key, value := range row {
		column := normalizeRestoreColumnName(table, key)
		if column == "" {
			continue
		}
		if column == "count" {
			mergeRestoreCount(value, result, columns)
			continue
		}
		columnInfo, ok := columns[column]
		if !ok {
			continue
		}
		normalizedValue, ok := normalizeRestoreValue(table, column, value, columnInfo.DBType)
		if !ok {
			continue
		}
		result[column] = normalizedValue
	}
	ensureRestoreBaseTimestamps(result)
	return result
}

func normalizeRestoreColumnName(table, name string) string {
	table = strings.ToLower(strings.TrimSpace(table))
	raw := strings.TrimSpace(name)
	lower := strings.ToLower(raw)
	if lower == "" || lower == "__v" {
		return ""
	}
	if table == "options" && lower == "_id" {
		// options.id is AUTO_INCREMENT; importing mongo _id would break insert.
		return ""
	}

	snake := strings.ToLower(camelToSnake(raw))
	if tableAliases, ok := restoreColumnAliasesByTable[table]; ok {
		for _, key := range []string{lower, snake} {
			if mapped, exists := tableAliases[key]; exists {
				return mapped
			}
		}
	}
	for _, key := range []string{lower, snake} {
		if mapped, ok := restoreColumnAliases[key]; ok {
			return mapped
		}
	}
	if snake != "" {
		return snake
	}
	return lower
}

func normalizeRestoreValue(table, column string, value interface{}, dbType string) (interface{}, bool) {
	value = normalizeBSONValue(value)
	if value == nil {
		return nil, true
	}

	if isTimeLikeType(dbType) {
		if ts, ok := normalizeRestoreTime(value); ok {
			return ts, true
		}
		if strings.EqualFold(column, "updated_at") || isZeroLikeTimeValue(value) {
			return nil, true
		}
		return nil, false
	}

	if table == "comments" && column == "ref_type" {
		if s, ok := value.(string); ok {
			return normalizeRefType(s), true
		}
	}
	if table == "slug_trackers" && column == "type" {
		if s, ok := value.(string); ok {
			return normalizeRefType(s), true
		}
	}

	switch v := value.(type) {
	case map[string]interface{}, []interface{}:
		if isJSONLikeType(dbType) || isTextLikeType(dbType) {
			data, err := json.Marshal(v)
			if err != nil {
				return nil, false
			}
			return string(data), true
		}
		return nil, false
	case []byte:
		if isJSONLikeType(dbType) || isTextLikeType(dbType) {
			return string(v), true
		}
		return v, true
	case string:
		if isTimeLikeType(dbType) {
			if ts, ok := parseTimeString(v); ok {
				return ts, true
			}
			if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				if ts, ok := unixNumberToTime(n); ok {
					return ts, true
				}
			}
		}
		return v, true
	default:
		return v, true
	}
}

func normalizeRestoreTime(value interface{}) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		return v, true
	case primitive.DateTime:
		return v.Time(), true
	case int64:
		return unixNumberToTime(float64(v))
	case int32:
		return unixNumberToTime(float64(v))
	case int:
		return unixNumberToTime(float64(v))
	case float64:
		return unixNumberToTime(v)
	case float32:
		return unixNumberToTime(float64(v))
	case string:
		if ts, ok := parseTimeString(v); ok {
			return ts, true
		}
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return unixNumberToTime(n)
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

func isZeroLikeTimeValue(value interface{}) bool {
	switch v := value.(type) {
	case int64:
		return v == 0
	case int32:
		return v == 0
	case int:
		return v == 0
	case float64:
		return v == 0
	case float32:
		return v == 0
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "" || s == "0" || s == "null" || s == "0000-00-00" || s == "0000-00-00 00:00:00"
	case time.Time:
		return v.IsZero()
	case primitive.DateTime:
		return v.Time().IsZero()
	default:
		return false
	}
}

func ensureRestoreBaseTimestamps(row map[string]interface{}) {
	updated, hasUpdated := row["updated_at"]
	if !hasUpdated {
		return
	}
	if updated == nil {
		return
	}
	if updatedAt, ok := updated.(time.Time); ok {
		if updatedAt.IsZero() {
			row["updated_at"] = nil
		}
		return
	}
	row["updated_at"] = nil
}

func mergeRestoreCount(value interface{}, row map[string]interface{}, columns map[string]tableColumn) {
	countMap, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	if _, ok := columns["read_count"]; ok {
		read, exists := countMap["read"]
		if !exists {
			read, exists = countMap["reads"]
		}
		if exists {
			row["read_count"] = normalizeBSONValue(read)
		}
	}
	if _, ok := columns["like_count"]; ok {
		like, exists := countMap["like"]
		if !exists {
			like, exists = countMap["likes"]
		}
		if exists {
			row["like_count"] = normalizeBSONValue(like)
		}
	}
}

func normalizeRefType(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	if mapped, ok := restoreRefTypeAliases[key]; ok {
		return mapped
	}
	return key
}

func migrateLegacyOptions(tx *gorm.DB) error {
	type optionRow struct {
		Name  string
		Value string
	}

	var options []optionRow
	if err := tx.Table("options").Select("name, value").Find(&options).Error; err != nil {
		return err
	}
	if len(options) == 0 {
		return nil
	}

	sectionData := map[string]interface{}{}
	for _, option := range options {
		sectionKey, ok := mapLegacyOptionToConfigSection(option.Name)
		if !ok {
			continue
		}
		sectionData[sectionKey] = normalizeLegacyOptionValue(parseLegacyOptionValue(option.Value))
	}
	if len(sectionData) == 0 {
		return nil
	}

	defaultCfg := config.DefaultFullConfig()
	cfgRaw, _ := json.Marshal(defaultCfg)
	merged := map[string]interface{}{}
	_ = json.Unmarshal(cfgRaw, &merged)

	var existing optionRow
	if err := tx.Table("options").Select("name, value").Where("name = ?", "configs").First(&existing).Error; err == nil {
		_ = json.Unmarshal([]byte(existing.Value), &merged)
	}

	for sectionKey, sectionValue := range sectionData {
		merged[sectionKey] = sectionValue
	}

	mergedRaw, err := json.Marshal(merged)
	if err != nil {
		return err
	}

	if err := tx.Exec("DELETE FROM `options` WHERE `name` = ?", "configs").Error; err != nil {
		return err
	}
	return tx.Table("options").Create(map[string]interface{}{
		"name":  "configs",
		"value": string(mergedRaw),
	}).Error
}

func mapLegacyOptionToConfigSection(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", false
	}
	snake := strings.ToLower(camelToSnake(trimmed))
	squashed := strings.ReplaceAll(snake, "_", "")
	if section, ok := legacyOptionKeyAliases[snake]; ok {
		return section, true
	}
	if section, ok := legacyOptionKeyAliases[squashed]; ok {
		return section, true
	}
	return "", false
}

func parseLegacyOptionValue(raw string) interface{} {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	var asJSON interface{}
	if err := json.Unmarshal([]byte(s), &asJSON); err == nil {
		return asJSON
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(strings.ToLower(s)); err == nil {
		return b
	}
	return s
}

func normalizeLegacyOptionValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, item := range v {
			normalizedKey := strings.ToLower(camelToSnake(strings.TrimSpace(key)))
			if normalizedKey == "" {
				continue
			}
			out[normalizedKey] = normalizeLegacyOptionValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = normalizeLegacyOptionValue(item)
		}
		return out
	default:
		return value
	}
}

func importLegacyEmailTemplates(tx *gorm.DB, zr *zip.Reader) error {
	templateNameToOption := map[string]string{
		"owner.template.ejs":      "email_template_owner",
		"guest.template.ejs":      "email_template_guest",
		"newsletter.template.ejs": "email_template_newsletter",
	}

	for _, file := range zr.File {
		normalized := strings.ToLower(strings.ReplaceAll(file.Name, "\\", "/"))
		if !strings.HasPrefix(normalized, "backup_data/assets/email-template/") {
			continue
		}
		optionName, ok := templateNameToOption[path.Base(normalized)]
		if !ok {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			continue
		}
		payload, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			continue
		}

		content := strings.TrimSpace(string(payload))
		if content == "" {
			continue
		}

		if err := tx.Exec("DELETE FROM `options` WHERE `name` = ?", optionName).Error; err != nil {
			return err
		}
		if err := tx.Table("options").Create(map[string]interface{}{
			"name":  optionName,
			"value": content,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}
