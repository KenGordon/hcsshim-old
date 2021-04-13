package hcs

import (
	"context"

	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/osversion"
	"github.com/pkg/errors"
)

func (uvm *utilityVM) SetUEFIBoot(ctx context.Context, dir string, path string, args string) error {
	if uvm.state != vm.StatePreCreated {
		return vm.ErrNotInPreCreatedState
	}
	uvm.doc.VirtualMachine.Chipset.Uefi = &hcsschema.Uefi{
		BootThis: &hcsschema.UefiBootEntry{
			DevicePath:    path,
			DeviceType:    "VmbFs",
			VmbFsRootPath: dir,
			OptionalData:  args,
		},
	}
	return nil
}

func (uvm *utilityVM) SetLinuxKernelDirectBoot(ctx context.Context, kernel string, initRD string, cmd string) error {
	if uvm.state != vm.StatePreCreated {
		return vm.ErrNotInPreCreatedState
	}
	if osversion.Get().Build < 18286 {
		return errors.New("Linux kernel direct boot requires at least Windows version 18286")
	}
	uvm.doc.VirtualMachine.Chipset.LinuxKernelDirect = &hcsschema.LinuxKernelDirect{
		KernelFilePath: kernel,
		InitRdPath:     initRD,
		KernelCmdLine:  cmd,
	}
	return nil
}
