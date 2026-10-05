package backup

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	appcfg "github.com/mx-space/core/internal/config"
	"go.uber.org/zap"
)

type fakeUploader struct {
	key     string
	payload []byte
	err     error
}

func (u *fakeUploader) Upload(_ context.Context, key string, payload []byte, _ string) (string, error) {
	u.key = key
	u.payload = payload
	if u.err != nil {
		return "", u.err
	}
	return "https://s3.example.com/" + key, nil
}

func newUploadTestHandler(uploader S3Uploader, initErr error) *Handler {
	return &Handler{
		logger: zap.NewNop(),
		newUploader: func(appcfg.S3Options) (S3Uploader, error) {
			if initErr != nil {
				return nil, initErr
			}
			return uploader, nil
		},
	}
}

func TestUploadBackupArtifact(t *testing.T) {
	now := time.Date(2026, 10, 5, 1, 2, 3, 0, time.Local)
	artifact := &backupArtifact{Filename: "backup-x.zip", Buffer: bytes.NewBufferString("zip")}
	enabled := appcfg.BackupOptions{Enable: true, Path: "Backups/{Y}/{m}/backup-{Y}{m}{d}-{h}{i}{s}.zip"}

	t.Run("disabled", func(t *testing.T) {
		up := &fakeUploader{}
		got := newUploadTestHandler(up, nil).uploadBackupArtifact(context.Background(), appcfg.BackupOptions{}, appcfg.S3Options{}, artifact, now)
		if got.Status != S3UploadSkipped || up.key != "" {
			t.Fatalf("expected skipped without upload, got %+v (key %q)", got, up.key)
		}
	})

	t.Run("invalid config", func(t *testing.T) {
		got := newUploadTestHandler(nil, errors.New("incomplete s3 config")).uploadBackupArtifact(context.Background(), enabled, appcfg.S3Options{}, artifact, now)
		if got.Status != S3UploadFailed || got.Error != "incomplete s3 config" {
			t.Fatalf("expected config failure, got %+v", got)
		}
	})

	t.Run("upload error", func(t *testing.T) {
		up := &fakeUploader{err: errors.New("s3 upload failed: 403")}
		got := newUploadTestHandler(up, nil).uploadBackupArtifact(context.Background(), enabled, appcfg.S3Options{}, artifact, now)
		if got.Status != S3UploadFailed || got.Error != "s3 upload failed: 403" || got.Key != "Backups/2026/10/backup-20261005-010203.zip" {
			t.Fatalf("expected upload failure with key, got %+v", got)
		}
	})

	t.Run("uploaded", func(t *testing.T) {
		up := &fakeUploader{}
		got := newUploadTestHandler(up, nil).uploadBackupArtifact(context.Background(), enabled, appcfg.S3Options{}, artifact, now)
		want := "Backups/2026/10/backup-20261005-010203.zip"
		if got.Status != S3UploadUploaded || got.Key != want || got.URL != "https://s3.example.com/"+want {
			t.Fatalf("unexpected result %+v", got)
		}
		if string(up.payload) != "zip" {
			t.Fatalf("uploaded payload mismatch: %q", up.payload)
		}
	})
}

func TestRenderBackupObjectKey(t *testing.T) {
	now := time.Date(2026, 1, 18, 1, 0, 1, 0, time.Local)
	cases := map[string]string{
		"Backups/{Y}/{m}/backup-{Y}{m}{d}-{h}{i}{s}.zip": "Backups/2026/01/backup-20260118-010001.zip",
		"backups/{y}{m}{d}-{H}{M}{s}.zip":                "backups/260118-010001.zip",
		"/backups//{Y}/{filename}":                       "backups/2026/backup-a.zip",
		"":                                               "backups/2026/01/backup-a.zip",
	}
	for tpl, want := range cases {
		if got := renderBackupObjectKey(tpl, "backup-a.zip", now); got != want {
			t.Errorf("renderBackupObjectKey(%q) = %q, want %q", tpl, got, want)
		}
	}
}
