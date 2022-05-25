package net

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
)

// VethEndpoint gathers a network pair and its properties.
type Endpoint struct {
}

// Properties returns properties for the veth interface in the network pair.
func (endpoint *Endpoint) Properties() NetworkInfo {
	return NetworkInfo{}
}

// Name returns name of the veth interface in the network pair.
func (endpoint *Endpoint) Name() string {
	return ""
}

func (endpoint *Endpoint) VirtPair() string {
	return ""
}

// HardwareAddr returns the mac address that is assigned to the tap interface
// in th network pair.
func (endpoint *Endpoint) HardwareAddr() string {
	return ""
}

// NetworkPair returns the network pair of the endpoint.
func (endpoint *Endpoint) NetworkPair() *NetworkInterfacePair {
	return nil
}

// SetProperties sets the properties for the endpoint.
func (endpoint *Endpoint) SetProperties(properties NetworkInfo) {
}

// Attach for veth endpoint bridges the network pair and adds the
// tap interface of the network pair to the hypervisor.
func (endpoint *Endpoint) Attach(ctx context.Context, uvmb vm.UVMBuilder) error {
	return nil
}

// Detach for the veth endpoint tears down the tap and bridge
// created for the veth interface.
func (endpoint *Endpoint) Detach(ctx context.Context, netNsName string, virt vm.UVM) error {
	return nil
}
