package remotevm

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vmservice"
	"github.com/pkg/errors"
)

func (uvm *remoteVM) SetMemoryLimit(ctx context.Context, memoryMB uint64) error {
	if uvm.state != vm.StatePreCreated {
		return errors.New("VM is not in pre-created state")
	}
	if uvm.config.MemoryConfig == nil {
		uvm.config.MemoryConfig = &vmservice.MemoryConfig{}
	}
	uvm.config.MemoryConfig.MemoryMb = memoryMB
	return nil
}

func (uvm *remoteVM) SetMemoryConfig(ctx context.Context, config *vm.MemoryConfig) error {
	return vm.ErrNotSupported
}

func (uvm *remoteVM) SetMMIOConfig(ctx context.Context, lowGapMB uint64, highBaseMB uint64, highGapMB uint64) error {
	return vm.ErrNotSupported
}
