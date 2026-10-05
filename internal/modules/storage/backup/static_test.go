package backup

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticFilesRoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "image"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "image", "a.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	if err := addStaticFiles(w, src); err != nil {
		t.Fatal(err)
	}
	evil, _ := w.Create(backupStaticDir + "../../escape.txt")
	_, _ = evil.Write([]byte("x"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dst := filepath.Join(root, "static")
	if err := restoreStaticFiles(zr, dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "image", "a.png")); err != nil || string(data) != "png" {
		t.Fatalf("restored file mismatch: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("path traversal entry escaped the static dir")
	}
}
