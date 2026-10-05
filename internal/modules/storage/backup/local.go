package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
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
	filePath := filepath.Join(backupDir, filename)
	if err := os.WriteFile(filePath, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	return &backupArtifact{
		Filename: filename,
		Path:     filePath,
		Buffer:   buf,
	}, nil
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
		Size:     formatSize(int64(artifact.Buffer.Len())),
	}
	cfg, err := h.loadConfig()
	if err != nil {
		h.logger.Warn("读取备份配置失败，跳过 S3 上传", zap.Error(err))
		result.S3 = S3UploadResult{Status: S3UploadFailed, Error: err.Error()}
		return result, artifact, nil
	}
	result.S3 = h.uploadBackupArtifact(ctx, cfg.BackupOptions, cfg.S3Options, artifact, now)
	return result, artifact, nil
}

func (h *Handler) uploadBackupArtifact(
	ctx context.Context,
	backupOpts config.BackupOptions,
	s3Opts config.S3Options,
	artifact *backupArtifact,
	now time.Time,
) S3UploadResult {
	if !backupOpts.Enable {
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
	h.logger.Info(fmt.Sprintf("上传备份到 S3：%s", key))
	url, err := uploader.Upload(ctx, key, artifact.Buffer.Bytes(), "application/zip")
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
