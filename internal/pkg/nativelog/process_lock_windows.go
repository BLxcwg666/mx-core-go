//go:build windows

package nativelog

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

var (
	logLockOnce   sync.Once
	logLockHandle windows.Handle
	logLockErr    error
)

func withProcessLogLock(fn func() error) error {
	h, err := getProcessLogLockHandle()
	if err != nil {
		return err
	}

	state, err := windows.WaitForSingleObject(h, windows.INFINITE)
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 && state != windows.WAIT_ABANDONED {
		return fmt.Errorf("wait process log lock: unexpected state %d", state)
	}

	defer func() {
		_ = windows.ReleaseMutex(h)
	}()
	return fn()
}

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
