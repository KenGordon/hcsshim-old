package hcs

import (
	"context"
	"fmt"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/requesttype"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
)

func (uvm *utilityVM) AddNIC(ctx context.Context, nicID guid.GUID, endpointID string, mac string) error {
	request := hcsschema.ModifySettingRequest{
		RequestType:  requesttype.Add,
		ResourcePath: fmt.Sprintf("VirtualMachine/Devices/NetworkAdapters/%s", nicID.String()),
		Settings: hcsschema.NetworkAdapter{
			EndpointId: endpointID,
			MacAddress: mac,
		},
	}
	return uvm.cs.Modify(ctx, request)
}
