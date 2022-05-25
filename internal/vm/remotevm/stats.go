package remotevm

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/pkg/service/stats"
)

func (uvm *utilityVM) Stats(ctx context.Context) (*stats.VirtualMachineStatistics, error) {
	return nil, vm.ErrNotSupported
}
