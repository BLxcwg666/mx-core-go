//go:build !windows

package cluster

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// ListenTCP creates a TCP listener. When reusePort=true it enables SO_REUSEPORT.
func ListenTCP(addr string, reusePort bool) (net.Listener, error) {
	if !reusePort {
		return net.Listen("tcp", addr)
	}

	lc := net.ListenConfig{
		Control: func(_ string, _ string, c syscall.RawConn) error {
			var controlErr error
			if err := c.Control(func(fd uintptr) {
				if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
					controlErr = err
					return
				}
				if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
					controlErr = err
					return
				}
			}); err != nil {
				return err
			}
			return controlErr
		},
	}
	return lc.Listen(context.Background(), "tcp", addr)
}
