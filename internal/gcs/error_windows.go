package gcs

import (
	"net"
	"os"
	"syscall"
)

func isLocalDisconnectError(err error) bool {
	if o, ok := err.(*net.OpError); ok {
		if s, ok := o.Err.(*os.SyscallError); ok {
			return s.Err == syscall.WSAECONNABORTED
		}
	}
	return false
}
