package remotevm

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
)

func (uvm *remoteVM) AddDevice(ctx context.Context) error {
	return vm.ErrNotSupported
}
