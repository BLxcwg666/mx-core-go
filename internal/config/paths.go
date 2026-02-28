package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ExecutableDir returns the directory where the current executable resides.
func ExecutableDir() string {
	exe, err := os.Executable()
	if err == nil && strings.TrimSpace(exe) != "" {
		if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil && strings.TrimSpace(resolved) != "" {
			exe = resolved
		}
		return filepath.Dir(exe)
	}

	if wd, wdErr := os.Getwd(); wdErr == nil && strings.TrimSpace(wd) != "" {
		return wd
	}
	return "."
}

// ResolveRuntimePath resolves runtime directories against the executable directory.
func ResolveRuntimePath(raw string, fallbackSubdir string) string {
	target := strings.TrimSpace(raw)
	if target == "" {
		target = strings.TrimSpace(fallbackSubdir)
		if target == "" {
			return ExecutableDir()
		}
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(ExecutableDir(), target))
}
