package hcs

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
)

func (uvm *utilityVM) SetProcessorCount(ctx context.Context, count uint32) error {
	if uvm.state != vm.StatePreCreated {
		return vm.ErrNotInPreCreatedState
	}
	uvm.doc.VirtualMachine.ComputeTopology.Processor.Count = int32(count)
	return nil
}
