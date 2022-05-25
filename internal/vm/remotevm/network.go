package remotevm

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vm/remotevm/net"
)

// Endpoint represents a physical or virtual network interface.
type Endpoint interface {
	Properties() net.NetworkInfo
	Name() string
	VirtPair() string
	HardwareAddr() string
	NetworkPair() *net.NetworkInterfacePair
	SetProperties(net.NetworkInfo)
	Attach(context.Context, vm.UVMBuilder) error
	Detach(context.Context, string, vm.UVM) error
}
