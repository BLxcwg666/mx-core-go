//go:build !windows

package nativelog

func withProcessLogLock(fn func() error) error {
	return fn()
}
