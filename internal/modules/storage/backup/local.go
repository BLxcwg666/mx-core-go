package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func resolveBackupDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvBackupDir)); dir != "" {
		return config.ResolveRuntimePath(dir, "")
	}
	return config.ResolveRuntimePath("", "backups")
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

// createLocalBackupArtifact writes the archive straight to the backup directory, so uploaded
// files in it do not have to fit in memory.
func (h *Handler) createLocalBackupArtifact(now time.Time) (*backupArtifact, error) {
	backupDir := resolveBackupDir()
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, err
	}

	filename := fmt.Sprintf("backup-%s.zip", now.Format("2006-01-02T15-04-05"))
	filePath := filepath.Join(backupDir, filename)
	tmpPath := filePath + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return nil, err
	}
	if err := h.createBackupZip(file); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	return &backupArtifact{
		Filename: filename,
		Path:     filePath,
		Size:     info.Size(),
	}, nil
}

// createBackupZip exports the tables as BSON plus the uploaded files into a ZIP archive.
// Any failure aborts the backup: a backup silently missing a table is worse than none.
func (h *Handler) createBackupZip(out io.Writer) error {
	w := zip.NewWriter(out)

	exportedTables := make([]string, 0, len(backupTableNames))
	for _, table := range backupTableNames {
		if _, skip := backupExportSkip[table]; skip {
			continue
		}
		var rows []map[string]interface{}
		if err := h.db.Table(table).Find(&rows).Error; err != nil {
			return fmt.Errorf("export table %s: %w", table, err)
		}

		payload, err := encodeBSONRows(rows)
		if err != nil {
			return fmt.Errorf("encode table %s: %w", table, err)
		}

		f, err := w.Create(path.Join(backupDBDir, table+".bson"))
		if err != nil {
			return err
		}
		if len(payload) > 0 {
			if _, err := f.Write(payload); err != nil {
				return err
			}
		}

		exportedTables = append(exportedTables, table)
	}

	if err := addStaticFiles(w, resolveStaticDir()); err != nil {
		return fmt.Errorf("export uploaded files: %w", err)
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

	return w.Close()
}

func resolveStaticDir() string {
	if dir := strings.TrimSpace(os.Getenv(envStaticDir)); dir != "" {
		return config.ResolveRuntimePath(dir, "")
	}
	return config.ResolveRuntimePath("", "static")
}

// envStaticDir mirrors file.EnvStaticDir (importing the file module here would be an import cycle).
const envStaticDir = "MX_STATIC_DIR"

func addStaticFiles(w *zip.Writer, staticDir string) error {
	if _, err := os.Stat(staticDir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(staticDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(staticDir, p)
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := w.Create(backupStaticDir + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, src)
		return err
	})
}

// CreateBackup writes a local backup and uploads it to S3 when backup upload is enabled.
// The local backup is kept even if the upload fails; the upload failure is returned as an error.
func CreateBackup(ctx context.Context, db *gorm.DB, cfgSvc *configs.Service, logger *zap.Logger) (*BackupResult, error) {
	h := NewHandler(db, cfgSvc, nil, WithLogger(logger))
	result, _, err := h.createBackup(ctx)
	if err != nil {
		return nil, err
	}
	if result.S3.Status == S3UploadFailed {
		return result, fmt.Errorf("本地备份 %s 已创建，但上传 S3 失败：%s", result.Filename, result.S3.Error)
	}
	return result, nil
}

// createBackup never fails because of S3: the upload outcome is reported in the result.
func (h *Handler) createBackup(ctx context.Context) (*BackupResult, *backupArtifact, error) {
	h.logger.Info("备份数据库中...")
	now := time.Now()
	artifact, err := h.createLocalBackupArtifact(now)
	if err != nil {
		h.logger.Warn("备份失败", zap.Error(err))
		return nil, nil, err
	}
	h.logger.Info(fmt.Sprintf("备份成功：%s", artifact.Filename))

	result := &BackupResult{
		Filename: artifact.Filename,
		Size:     formatSize(artifact.Size),
	}
	cfg, err := h.loadConfig()
	if err != nil {
		h.logger.Warn("读取备份配置失败，跳过 S3 上传", zap.Error(err))
		result.S3 = S3UploadResult{Status: S3UploadFailed, Error: err.Error()}
		return result, artifact, nil
	}
	result.S3 = h.uploadBackupArtifact(ctx, cfg.BackupOptions, cfg.S3Options, artifact, now)
	h.pruneLocalBackups(cfg.BackupOptions.KeepCount)
	return result, artifact, nil
}

// localBackupPattern matches the archives createLocalBackupArtifact writes; retention never
// touches other files placed in the backup directory.
var localBackupPattern = regexp.MustCompile(`^backup-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.zip$`)

// pruneLocalBackups keeps the newest keep backups (by the timestamp in their name); keep <= 0 keeps all.
func (h *Handler) pruneLocalBackups(keep int) {
	if keep <= 0 {
		return
	}
	backupDir := resolveBackupDir()
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		h.logger.Warn("读取备份目录失败，跳过清理旧备份", zap.Error(err))
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && localBackupPattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(backupDir, name)); err != nil {
			h.logger.Warn("删除旧备份失败", zap.String("file", name), zap.Error(err))
			continue
		}
		h.logger.Info(fmt.Sprintf("已删除旧备份：%s", name))
	}
}

func (h *Handler) uploadBackupArtifact(
	ctx context.Context,
	backupOpts config.BackupOptions,
	s3Opts config.S3Options,
	artifact *backupArtifact,
	now time.Time,
) S3UploadResult {
	if !backupOpts.UploadToS3 {
		return S3UploadResult{Status: S3UploadSkipped}
	}

	newUploader := h.newUploader
	if newUploader == nil {
		newUploader = NewS3Uploader
	}
	uploader, err := newUploader(s3Opts)
	if err != nil {
		h.logger.Warn("S3 配置无效，备份未上传", zap.Error(err))
		return S3UploadResult{Status: S3UploadFailed, Error: err.Error()}
	}

	key := renderBackupObjectKey(backupOpts.Path, artifact.Filename, now)
	payload, err := artifact.bytes()
	if err != nil {
		return S3UploadResult{Status: S3UploadFailed, Key: key, Error: err.Error()}
	}
	h.logger.Info(fmt.Sprintf("上传备份到 S3：%s", key))
	url, err := uploader.Upload(ctx, key, payload, "application/zip")
	if err != nil {
		h.logger.Warn("S3 上传失败", zap.String("key", key), zap.Error(err))
		return S3UploadResult{Status: S3UploadFailed, Key: key, Error: err.Error()}
	}
	h.logger.Info("S3 上传成功")
	return S3UploadResult{Status: S3UploadUploaded, Key: key, URL: url}
}

func (h *Handler) loadConfig() (*config.FullConfig, error) {
	if h.cfgSvc == nil {
		return nil, fmt.Errorf("config service is unavailable")
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("configs not initialized")
	}
	return cfg, nil
}
