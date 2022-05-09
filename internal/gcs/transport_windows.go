package gcs

import (
	"net"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
)

// VMSockIoListen returns an implementation of IoListenFunc that listens
// on the specified vsock port for the VM specified by `vmID`.
func VMSockIoListen(vmID guid.GUID) IoListenFunc {
	return func(port uint32) (net.Listener, error) {
		return winio.ListenHvsock(&winio.HvsockAddr{
			VMID:      vmID,
			ServiceID: winio.VsockServiceID(port),
		})
	}
}
