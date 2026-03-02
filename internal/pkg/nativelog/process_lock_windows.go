//go:build windows

package nativelog

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var (
	logLockOnce   sync.Once
	logLockHandle windows.Handle
	logLockErr    error
)

const processLogLockWait = 300 * time.Millisecond

func withProcessLogLock(fn func() error) error {
	return withProcessLogLockTimeout(processLogLockWait, fn)
}

func withProcessLogLockNoWait(fn func() error) error {
	return withProcessLogLockTimeout(0, fn)
}

func withProcessLogLockTimeout(timeout time.Duration, fn func() error) error {
	h, err := getProcessLogLockHandle()
	if err != nil {
		return err
	}

	waitMs := uint32(0)
	if timeout > 0 {
		waitMs = uint32(timeout / time.Millisecond)
		if waitMs == 0 {
			waitMs = 1
		}
	}

	state, err := windows.WaitForSingleObject(h, waitMs)
	if err != nil {
		return err
	}
	if state == uint32(windows.WAIT_TIMEOUT) {
		return errProcessLogLockTimeout
	}
	if state != windows.WAIT_OBJECT_0 && state != uint32(windows.WAIT_ABANDONED) {
		return fmt.Errorf("wait process log lock: unexpected state %d", state)
	}

	defer func() {
		_ = windows.ReleaseMutex(h)
	}()
	return fn()
}

var errProcessLogLockTimeout = errors.New("process log lock timeout")

func getProcessLogLockHandle() (windows.Handle, error) {
	logLockOnce.Do(func() {
		name, err := windows.UTF16PtrFromString(`Global\mx-core-go-nativelog-lock`)
		if err != nil {
			logLockErr = err
			return
		}
		h, err := windows.CreateMutex(nil, false, name)
		if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			logLockErr = err
			return
		}
		logLockHandle = h
	})
	if logLockErr != nil {
		return 0, logLockErr
	}
	if logLockHandle == 0 {
		return 0, errors.New("invalid process log lock handle")
	}
	return logLockHandle, nil
}
