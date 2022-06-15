package transport

import (
	"net"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/linuxkit/virtsock/pkg/vsock"
)

// VMSockIoListen returns an implementation of IoListenFunc that listens
// on the specified vsock port for the VM specified by `vmID`.
func VMSockIoListen(vmID guid.GUID) IoListenFunc {
	return func(port uint32) (net.Listener, error) {
		return vsock.Listen(vsock.CIDHost, port)
	}
}
