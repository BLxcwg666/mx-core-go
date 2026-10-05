package backup

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"go.uber.org/zap"
)

func TestPruneLocalBackupsKeepsNewestOwnArchives(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvBackupDir, dir)
	files := []string{
		"backup-2026-10-01T01-00-00.zip",
		"backup-2026-10-02T01-00-00.zip",
		"backup-2026-10-03T01-00-00.zip",
		"backup-2026-10-04T01-00-00.zip",
		"my-own.zip",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	h := &Handler{logger: zap.NewNop()}
	h.pruneLocalBackups(0)
	if entries, _ := os.ReadDir(dir); len(entries) != len(files) {
		t.Fatalf("keep=0 must not delete anything, %d files left", len(entries))
	}

	h.pruneLocalBackups(2)
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := []string{"backup-2026-10-03T01-00-00.zip", "backup-2026-10-04T01-00-00.zip", "my-own.zip"}
	if len(left) != len(want) {
		t.Fatalf("got %v, want %v", left, want)
	}
	for i := range want {
		if left[i] != want[i] {
			t.Fatalf("got %v, want %v", left, want)
		}
	}
}
