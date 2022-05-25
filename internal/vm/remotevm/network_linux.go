package remotevm

import (
	"context"

	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vm/remotevm/net"
	"github.com/Microsoft/hcsshim/internal/vmservice"
)

func InitialNetSetup(ctx context.Context, netNS string, uvmb vm.UVMBuilder) (Endpoint, error) {
	return net.SetupNet(ctx, netNS, uvmb)
}

func (uvmb *utilityVMBuilder) AddNIC(ctx context.Context, nicID, endpointID, macAddr string) error {
	nic := &vmservice.NICConfig{
		NicID:      nicID,
		MacAddress: macAddr,
		Backend: &vmservice.NICConfig_Tap{
			Tap: &vmservice.TapBackend{
				Name: endpointID,
			},
		},
	}
	uvmb.config.DevicesConfig.NicConfig = append(uvmb.config.DevicesConfig.NicConfig, nic)
	return nil
}

func (uvmb *utilityVMBuilder) RemoveNIC(ctx context.Context, nicID, endpointID, macAddr string) error {
	return nil
}

func (uvmb *utilityVMBuilder) UpdateNIC(ctx context.Context, nicID string, nic *hcsschema.NetworkAdapter) error {
	return nil
}
