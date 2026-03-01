//go:build linux

package proctitle

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const linuxProcNameMax = 15

// Set applies a short process title on Linux via PR_SET_NAME.
func Set(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("empty process title")
	}

	if len(os.Args) > 0 {
		os.Args[0] = title
	}

	b := make([]byte, linuxProcNameMax+1)
	copy(b, []byte(title))

	return unix.Prctl(unix.PR_SET_NAME, uintptr(unsafe.Pointer(&b[0])), 0, 0, 0)
}
