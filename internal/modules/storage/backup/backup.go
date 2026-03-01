package backup

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	pkgredis "github.com/mx-space/core/internal/pkg/redis"
	"github.com/mx-space/core/internal/pkg/response"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"gorm.io/gorm"
)

const backupRootDir = "mx-space-go"
const backupDBDir = backupRootDir + "/db"
const backupManifestFile = backupRootDir + "/manifest.json"
const backupFormat = "mx-core-go-bson"
const backupFormatVersion = 1
const defaultS3PathTemplate = "backups/{Y}/{m}/{filename}"
const EnvBackupDir = "MX_BACKUP_DIR"

var backupTableNames = []string{
	"users",
	"user_sessions",
	"api_tokens",
	"oauth2_tokens",
	"authn_credentials",
	"readers",
	"categories",
	"topics",
	"posts",
	"notes",
	"pages",
	"comments",
	"recentlies",
	"drafts",
	"draft_histories",
	"ai_summaries",
	"ai_deep_readings",
	"analyzes",
	"activities",
	"slug_trackers",
	"file_references",
	"webhooks",
	"webhook_events",
	"snippets",
	"projects",
	"links",
	"says",
	"subscribes",
	"meta_presets",
	"serverless_storages",
	"options",
}

var backupTableNameSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(backupTableNames))
	for _, table := range backupTableNames {
		set[table] = struct{}{}
	}
	return set
}()

func resolveBackupDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvBackupDir)); dir != "" {
		return config.ResolveRuntimePath(dir, "")
	}
	return config.ResolveRuntimePath("", "backups")
}

var restoreTableAliases = map[string]string{
	"metapresets":        "meta_presets",
	"sessions":           "user_sessions",
	"serverlessstorages": "serverless_storages",
	"authns":             "authn_credentials",
	"analyze_logs":       "analyzes",
	"recently":           "recentlies",
	"subscribers":        "subscribes",
}

var restoreColumnAliases = map[string]string{
	"_id":           "id",
	"created":       "created_at",
	"modified":      "updated_at",
	"createdat":     "created_at",
	"updatedat":     "updated_at",
	"userid":        "user_id",
	"ipaddress":     "ip",
	"useragent":     "ua",
	"reftype":       "ref_type",
	"refid":         "ref_id",
	"ref":           "ref_id",
	"parent":        "parent_id",
	"targetid":      "target_id",
	"commentsindex": "comments_index",
	"iswhispers":    "is_whispers",
	"parentid":      "parent_id",
	"readerid":      "reader_id",
	"publicat":      "public_at",
	"topicid":       "topic_id",
	"categoryid":    "category_id",
	"pinorder":      "pin_order",
	"readcount":     "read_count",
	"likecount":     "like_count",
	"nid":           "n_id",
}

var restoreColumnAliasesByTable = map[string]map[string]string{
	"notes": {
		"password": "password_hash",
	},
}

var restoreRefTypeAliases = map[string]string{
	"posts":      "post",
	"post":       "post",
	"notes":      "note",
	"note":       "note",
	"pages":      "page",
	"page":       "page",
	"recently":   "recently",
	"recentlies": "recently",
}

var legacyOptionKeyAliases = map[string]string{
	"seo":                          "seo",
	"url":                          "url",
	"mailoptions":                  "mail_options",
	"commentoptions":               "comment_options",
	"backupoptions":                "backup_options",
	"baidusearchoptions":           "baidu_search_options",
	"algoliasearchoptions":         "algolia_search_options",
	"adminextra":                   "admin_extra",
	"friendlinkoptions":            "friend_link_options",
	"s3options":                    "s3_options",
	"imagebedoptions":              "image_bed_options",
	"imagestorageoptions":          "image_storage_options",
	"textoptions":                  "text_options",
	"bingsearchoptions":            "bing_search_options",
	"meilisearchoptions":           "meili_search_options",
	"featurelist":                  "feature_list",
	"barkoptions":                  "bark_options",
	"authsecurity":                 "auth_security",
	"ai":                           "ai",
	"oauth":                        "oauth",
	"thirdpartyserviceintegration": "third_party_service_integration",
}

type Handler struct {
	db     *gorm.DB
	cfgSvc *configs.Service
	rc     *pkgredis.Client
}

type backupManifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	Engine    string    `json:"engine"`
	CreatedAt time.Time `json:"created_at"`
	Tables    []string  `json:"tables"`
}

type backupEntryCandidate struct {
	File   *zip.File
	Format string
}

type tableColumn struct {
	DBType string
}

func NewHandler(db *gorm.DB, cfgSvc *configs.Service, rc *pkgredis.Client) *Handler {
	return &Handler{db: db, cfgSvc: cfgSvc, rc: rc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/backups", authMW)

	g.GET("", h.list)
	g.GET("/new", h.createAndDownload)
	g.GET("/:filename", h.download)
	g.POST("", h.uploadAndRestore)
	g.POST("/rollback", h.uploadAndRestore)
	g.POST("/upload-to-s3", h.uploadToS3)
	g.PATCH("/rollback/:filename", h.rollback)
	g.PATCH("/:filename", h.rollback)
	g.DELETE("", h.delete)
	g.DELETE("/:filename", h.deleteOne)
}

type backupItem struct {
	Filename string `json:"filename"`
	Size     string `json:"size"`
}

// GET /backups
func (h *Handler) list(c *gin.Context) {
	items := listBackups()
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func listBackups() []backupItem {
	backupDir := resolveBackupDir()
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil
	}
	var items []backupItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, backupItem{
			Filename: e.Name(),
			Size:     formatSize(info.Size()),
		})
	}
	if items == nil {
		items = []backupItem{}
	}
	return items
}

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

// GET /backups/new
func (h *Handler) createAndDownload(c *gin.Context) {
	buf, err := h.createBackupZip()
	if err != nil {
		response.InternalError(c, err)
		return
	}

	backupDir := resolveBackupDir()
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		response.InternalError(c, err)
		return
	}
	filename := fmt.Sprintf("backup-%s.zip", time.Now().Format("2006-01-02T15-04-05"))
	path := filepath.Join(backupDir, filename)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		response.InternalError(c, err)
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}

// GET /backups/:filename
func (h *Handler) download(c *gin.Context) {
	filename := filepath.Base(c.Param("filename"))
	if !strings.HasSuffix(filename, ".zip") {
		response.BadRequest(c, "invalid filename")
		return
	}
	backupDir := resolveBackupDir()
	path := filepath.Join(backupDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/zip", data)
}

// POST /backups/rollback
func (h *Handler) uploadAndRestore(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "missing file")
		return
	}

	src, err := file.Open()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	defer src.Close()

	data, err := io.ReadAll(src)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		response.BadRequest(c, "invalid zip file")
		return
	}

	if err := RestoreFromZip(h.db, zr); err != nil {
		response.InternalError(c, err)
		return
	}
	h.invalidateRuntimeCaches(c)
	response.OK(c, gin.H{"message": "restore successful"})
}

// PATCH /backups/rollback/:filename
func (h *Handler) rollback(c *gin.Context) {
	filename := filepath.Base(c.Param("filename"))
	backupDir := resolveBackupDir()
	path := filepath.Join(backupDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			response.NotFound(c)
			return
		}
		response.InternalError(c, err)
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		response.BadRequest(c, "invalid zip file")
		return
	}

	if err := RestoreFromZip(h.db, zr); err != nil {
		response.InternalError(c, err)
		return
	}
	h.invalidateRuntimeCaches(c)
	response.OK(c, gin.H{"message": "rollback successful"})
}

func (h *Handler) invalidateRuntimeCaches(c *gin.Context) {
	if h.cfgSvc != nil {
		h.cfgSvc.Invalidate()
	}
	_ = h.rc.Raw().FlushDB(c.Request.Context())
}

// DELETE /backups
func (h *Handler) delete(c *gin.Context) {
	files := strings.TrimSpace(c.Query("files"))

	var body struct {
		Files string `json:"files"`
	}
	if files == "" {
		_ = c.ShouldBindJSON(&body)
		files = strings.TrimSpace(body.Files)
	}
	if files == "" {
		response.BadRequest(c, "missing files")
		return
	}

	backupDir := resolveBackupDir()
	filenames := strings.Split(files, ",")
	for _, name := range filenames {
		name = strings.TrimSpace(filepath.Base(name))
		if name == "" || !strings.HasSuffix(name, ".zip") {
			continue
		}
		os.Remove(filepath.Join(backupDir, name))
	}
	response.NoContent(c)
}

func (h *Handler) deleteOne(c *gin.Context) {
	filename := strings.TrimSpace(filepath.Base(c.Param("filename")))
	if filename == "" || !strings.HasSuffix(filename, ".zip") {
		response.BadRequest(c, "invalid filename")
		return
	}
	backupDir := resolveBackupDir()
	_ = os.Remove(filepath.Join(backupDir, filename))
	response.NoContent(c)
}

type backupArtifact struct {
	Filename string
	Path     string
	Buffer   *bytes.Buffer
}

func (h *Handler) createLocalBackupArtifact(now time.Time) (*backupArtifact, error) {
	buf, err := h.createBackupZip()
	if err != nil {
		return nil, err
	}
	backupDir := resolveBackupDir()
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, err
	}

	filename := fmt.Sprintf("backup-%s.zip", now.Format("2006-01-02T15-04-05"))
	path := filepath.Join(backupDir, filename)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	return &backupArtifact{
		Filename: filename,
		Path:     path,
		Buffer:   buf,
	}, nil
}

// POST /backups/upload-to-s3
func (h *Handler) uploadToS3(c *gin.Context) {
	if h.cfgSvc == nil {
		response.InternalError(c, fmt.Errorf("config service is unavailable"))
		return
	}

	cfg, err := h.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if cfg == nil {
		response.InternalError(c, fmt.Errorf("configs not initialized"))
		return
	}
	if !cfg.BackupOptions.Enable {
		// Keep compatibility: backup disabled means no-op.
		response.NoContent(c)
		return
	}

	uploader, err := newS3Uploader(cfg.S3Options)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	now := time.Now()
	artifact, err := h.createLocalBackupArtifact(now)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	key := renderBackupObjectKey(cfg.BackupOptions.Path, artifact.Filename, now)
	if _, err := uploader.Upload(c.Request.Context(), key, artifact.Buffer.Bytes(), "application/zip"); err != nil {
		response.InternalError(c, err)
		return
	}

	response.NoContent(c)
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

// createBackupZip exports all tables as BSON into a ZIP archive.
func (h *Handler) createBackupZip() (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	exportedTables := make([]string, 0, len(backupTableNames))
	for _, table := range backupTableNames {
		var rows []map[string]interface{}
		if err := h.db.Table(table).Find(&rows).Error; err != nil {
			continue
		}

		payload, err := encodeBSONRows(rows)
		if err != nil {
			continue
		}

		f, err := w.Create(path.Join(backupDBDir, table+".bson"))
		if err != nil {
			continue
		}
		if len(payload) > 0 {
			if _, err := f.Write(payload); err != nil {
				continue
			}
		}

		exportedTables = append(exportedTables, table)
	}

	manifest := backupManifest{
		Format:    backupFormat,
		Version:   backupFormatVersion,
		Engine:    "mysql",
		CreatedAt: time.Now().UTC(),
		Tables:    exportedTables,
	}
	if manifestData, err := json.Marshal(manifest); err == nil {
		if mf, err := w.Create(backupManifestFile); err == nil {
			_, _ = mf.Write(manifestData)
		}
	}

	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

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

// CreateLocalBackup creates a backup ZIP in the default backup directory.
func CreateLocalBackup(db *gorm.DB) error {
	h := &Handler{db: db}
	_, err := h.createLocalBackupArtifact(time.Now())
	return err
}
