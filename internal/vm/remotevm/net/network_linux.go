package net

import (
	"context"
	cryptoRand "crypto/rand"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/guest/network"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func SetupNet(ctx context.Context, name string, uvmb vm.UVMBuilder) (*VethEndpoint, error) {
	currNS, err := netns.GetFromPath(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace from name: %w", err)
	}
	defer currNS.Close()

	netlinkHandle, err := netlink.NewHandleAt(currNS)
	if err != nil {
		return nil, fmt.Errorf("failed to get handle to netns: %w", err)
	}
	defer netlinkHandle.Delete()

	linkList, err := netlinkHandle.LinkList()
	if err != nil {
		return nil, fmt.Errorf("failed to get link list: %w", err)
	}

	endpoint := &VethEndpoint{}
	for _, link := range linkList {
		netInfo, err := infoFromLink(netlinkHandle, link)
		if err != nil {
			return nil, fmt.Errorf("failed to get info from link: %w", err)
		}

		if len(netInfo.Addrs) == 0 {
			continue
		}

		// Skip any loopback interfaces:
		if (netInfo.Iface.Flags & net.FlagLoopback) != 0 {
			continue
		}

		if err := network.DoInNetNS(currNS, func() error {
			endpoint, err = addSingleEndpoint(ctx, netInfo, uvmb)
			return err
		}); err != nil {
			return nil, fmt.Errorf("failed run doinnetns: %w", err)
		}
	}
	return endpoint, nil
}

// NetlinkIface describes fully a network interface.
type NetlinkIface struct {
	netlink.LinkAttrs
	Type string
}

// DNSInfo describes the DNS setup related to a network interface.
type DNSInfo struct {
	Servers  []string
	Domain   string
	Searches []string
	Options  []string
}

// NetworkInfo gathers all information related to a network interface.
// It can be used to store the description of the underlying network.
type NetworkInfo struct {
	Iface     NetlinkIface
	DNS       DNSInfo
	Link      netlink.Link
	Addrs     []netlink.Addr
	Routes    []netlink.Route
	Neighbors []netlink.Neigh
	NicID     string
}

// NetworkInterface defines a network interface.
type NetworkInterface struct {
	Name     string
	HardAddr string
	Addrs    []netlink.Addr
}

// TapInterface defines a tap interface
type TapInterface struct {
	ID       string
	Name     string
	TAPIface NetworkInterface
	VMFds    []*os.File
	VhostFds []*os.File
}

// TuntapInterface defines a tap interface
type TuntapInterface struct {
	Name     string
	TAPIface NetworkInterface
}

// NetworkInterfacePair defines a pair between VM and virtual network interfaces.
type NetworkInterfacePair struct {
	TapInterface
	VirtIface NetworkInterface
}

func ConnectVMNetwork(ctx context.Context, endpoint *VethEndpoint) error {
	return setupTCFiltering(ctx, endpoint, 0)
}

// The endpoint type should dictate how the disconnection needs to happen.
func DisconnectVMNetwork(ctx context.Context, endpoint *VethEndpoint) error {
	return removeTCFiltering(ctx, endpoint)
}

// NetworkConfig is the network configuration related to a network.
type NetworkConfig struct {
	NetworkID         string
	NetworkCreated    bool
	DisableNewNetwork bool
}

func createNetworkInterfacePair(idx int, ifName string) (NetworkInterfacePair, error) {
	uniqueID, err := guid.NewV4()
	if err != nil {
		return NetworkInterfacePair{}, err
	}

	randomMacAddr, err := generateRandomPrivateMacAddr()
	if err != nil {
		return NetworkInterfacePair{}, fmt.Errorf("Could not generate random mac address: %s", err)
	}

	netPair := NetworkInterfacePair{
		TapInterface: TapInterface{
			ID:   uniqueID.String(),
			Name: fmt.Sprintf("br%d_hvl", idx),
			TAPIface: NetworkInterface{
				Name: fmt.Sprintf("tap%d_hvl", idx),
			},
		},
		VirtIface: NetworkInterface{
			Name:     fmt.Sprintf("eth%d", idx),
			HardAddr: randomMacAddr,
		},
	}

	if ifName != "" {
		netPair.VirtIface.Name = ifName
	}

	return netPair, nil
}

func generateRandomPrivateMacAddr() (string, error) {
	buf := make([]byte, 6)
	_, err := cryptoRand.Read(buf)
	if err != nil {
		return "", err
	}

	// Set the local bit for local addresses
	// Addresses in this range are local mac addresses:
	// x2-xx-xx-xx-xx-xx , x6-xx-xx-xx-xx-xx , xA-xx-xx-xx-xx-xx , xE-xx-xx-xx-xx-xx
	buf[0] = (buf[0] | 2) & 0xfe

	hardAddr := net.HardwareAddr(buf)
	return hardAddr.String(), nil
}
