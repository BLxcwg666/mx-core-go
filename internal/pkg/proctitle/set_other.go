//go:build !linux

package proctitle

import (
	"os"
	"strings"
)

// Set is best-effort on non-Linux platforms.
func Set(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	if len(os.Args) > 0 {
		os.Args[0] = title
	}
	return nil
}
