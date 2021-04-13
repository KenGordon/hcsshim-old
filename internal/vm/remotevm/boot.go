package remotevm

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vmservice"
	"github.com/pkg/errors"
)

func (uvm *remoteVM) SetLinuxKernelDirectBoot(ctx context.Context, kernel string, initRD string, cmd string) error {
	if uvm.state != vm.StatePreCreated {
		return errors.New("VM is not in pre-created state")
	}
	if uvm.config.BootConfig != nil {
		return vm.ErrAlreadySet
	}
	uvm.config.BootConfig = &vmservice.VMConfig_DirectBoot{
		DirectBoot: &vmservice.DirectBoot{
			KernelPath:    kernel,
			InitrdPath:    initRD,
			KernelCmdline: cmd,
		},
	}
	return nil
}

func (uvm *remoteVM) SetUEFIBoot(ctx context.Context, dir string, path string, args string) error {
	// TODO: dcantah
	return vm.ErrNotSupported
}
