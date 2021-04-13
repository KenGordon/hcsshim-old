package remotevm

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vmservice"
	"github.com/pkg/errors"
)

func (uvm *remoteVM) SetProcessorCount(ctx context.Context, count uint32) error {
	if uvm.state != vm.StatePreCreated {
		return errors.New("VM is not in pre-created state")
	}
	if uvm.config.ProcessorConfig == nil {
		uvm.config.ProcessorConfig = &vmservice.ProcessorConfig{}
	}
	uvm.config.ProcessorConfig.ProcessorCount = count
	return nil
}
