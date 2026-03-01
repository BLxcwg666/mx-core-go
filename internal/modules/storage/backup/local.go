package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mx-space/core/internal/config"
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

// CreateLocalBackup creates a backup ZIP in the default backup directory.
func CreateLocalBackup(db *gorm.DB) error {
	h := &Handler{db: db}
	_, err := h.createLocalBackupArtifact(time.Now())
	return err
}
