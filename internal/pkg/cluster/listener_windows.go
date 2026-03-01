//go:build windows

package cluster

import (
	"errors"
	"net"
)

// ListenTCP creates a TCP listener on Windows.
func ListenTCP(addr string, reusePort bool) (net.Listener, error) {
	if !reusePort {
		return net.Listen("tcp", addr)
	}
	return nil, errors.New("SO_REUSEPORT is not supported on windows")
}
