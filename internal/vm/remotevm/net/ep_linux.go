package net

import (
	"context"
	"fmt"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/guest/network"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// VethEndpoint gathers a network pair and its properties.
type VethEndpoint struct {
	EndpointProperties NetworkInfo
	NetPair            NetworkInterfacePair
	RxRateLimiter      bool
	TxRateLimiter      bool
}

func createVethNetworkEndpoint(idx int, ifName string) (*VethEndpoint, error) {
	if idx < 0 {
		return &VethEndpoint{}, fmt.Errorf("invalid network endpoint index: %d", idx)
	}
	netPair, err := createNetworkInterfacePair(idx, ifName)
	if err != nil {
		return nil, err
	}
	endpoint := &VethEndpoint{
		NetPair: netPair,
	}
	if ifName != "" {
		endpoint.NetPair.VirtIface.Name = ifName
	}
	return endpoint, nil
}

// Properties returns properties for the veth interface in the network pair.
func (endpoint *VethEndpoint) Properties() NetworkInfo {
	return endpoint.EndpointProperties
}

// Name returns name of the veth interface in the network pair.
func (endpoint *VethEndpoint) Name() string {
	return endpoint.NetPair.VirtIface.Name
}

func (endpoint *VethEndpoint) VirtPair() string {
	return endpoint.NetPair.TAPIface.Name
}

// HardwareAddr returns the mac address that is assigned to the tap interface
// in th network pair.
func (endpoint *VethEndpoint) HardwareAddr() string {
	return endpoint.NetPair.TAPIface.HardAddr
}

// NetworkPair returns the network pair of the endpoint.
func (endpoint *VethEndpoint) NetworkPair() *NetworkInterfacePair {
	return &endpoint.NetPair
}

// SetProperties sets the properties for the endpoint.
func (endpoint *VethEndpoint) SetProperties(properties NetworkInfo) {
	endpoint.EndpointProperties = properties
}

func (endpoint *VethEndpoint) updateNicID(nicID string) {
	endpoint.EndpointProperties.NicID = nicID
}

// Attach for veth endpoint bridges the network pair and adds the
// tap interface of the network pair to the hypervisor.
func (endpoint *VethEndpoint) Attach(ctx context.Context, uvmb vm.UVMBuilder) error {
	if err := ConnectVMNetwork(ctx, endpoint); err != nil {
		return err
	}

	net := uvmb.(vm.NetworkManager)
	nicID, err := guid.NewV4()
	if err != nil {
		return err
	}
	endpoint.updateNicID(nicID.String())

	log.G(ctx).WithFields(logrus.Fields{
		"nicID": nicID.String(),
		"tap":   endpoint.VirtPair(),
	}).Info("Adding network adapter to VM creation config")

	return net.AddNIC(ctx, nicID.String(), endpoint.VirtPair(), endpoint.HardwareAddr())
}

// Detach for the veth endpoint tears down the tap and bridge
// created for the veth interface.
func (endpoint *VethEndpoint) Detach(ctx context.Context, netNsName string, virt vm.UVM) error {
	currNS, err := netns.GetFromName(netNsName)
	if err != nil {
		return err
	}
	defer currNS.Close()
	return network.DoInNetNS(currNS, func() error {
		return DisconnectVMNetwork(ctx, endpoint)
	})
}

func addSingleEndpoint(ctx context.Context, netInfo NetworkInfo, uvmb vm.UVMBuilder) (*VethEndpoint, error) {
	var (
		err      error
		endpoint *VethEndpoint
	)
	if netInfo.Iface.Type == "veth" {
		endpoint, err = createVethNetworkEndpoint(0, netInfo.Iface.Name)
	} else {
		return nil, fmt.Errorf("Unsupported network interface: %s", netInfo.Iface.Type)
	}
	if err != nil {
		return nil, err
	}
	endpoint.SetProperties(netInfo)
	if err := endpoint.Attach(ctx, uvmb); err != nil {
		return nil, err
	}
	return endpoint, nil
}

func infoFromLink(handle *netlink.Handle, link netlink.Link) (NetworkInfo, error) {
	addrs, err := handle.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return NetworkInfo{}, err
	}

	routes, err := handle.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		return NetworkInfo{}, err
	}

	neighbors, err := handle.NeighList(link.Attrs().Index, netlink.FAMILY_ALL)
	if err != nil {
		return NetworkInfo{}, err
	}

	return NetworkInfo{
		Iface: NetlinkIface{
			LinkAttrs: *(link.Attrs()),
			Type:      link.Type(),
		},
		Addrs:     addrs,
		Routes:    routes,
		Neighbors: neighbors,
		Link:      link,
	}, nil
}

func validGuestRoute(route netlink.Route) bool {
	return route.Protocol != unix.RTPROT_KERNEL
}

func validGuestNeighbor(neigh netlink.Neigh) bool {
	// We add only static ARP entries
	return neigh.State == netlink.NUD_PERMANENT
}
