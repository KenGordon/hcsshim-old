package remotevm

import (
	"context"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/vm"
)

func (uvm *remoteVM) AddNIC(ctx context.Context, nicID guid.GUID, endpointID string, mac string) error {
	return vm.ErrNotSupported
}
