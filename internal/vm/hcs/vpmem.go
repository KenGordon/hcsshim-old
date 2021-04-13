package hcs

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Microsoft/hcsshim/internal/requesttype"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/pkg/errors"
)

func (uvm *utilityVM) AddVPMemController(ctx context.Context, maximumDevices uint32, maximumSizeBytes uint64) error {
	if uvm.state != vm.StatePreCreated {
		return vm.ErrNotInPreCreatedState
	}
	uvm.doc.VirtualMachine.Devices.VirtualPMem = &hcsschema.VirtualPMemController{
		MaximumCount:     maximumDevices,
		MaximumSizeBytes: maximumSizeBytes,
	}
	uvm.doc.VirtualMachine.Devices.VirtualPMem.Devices = make(map[string]hcsschema.VirtualPMemDevice)
	return nil
}

func (uvm *utilityVM) AddVPMemDevice(ctx context.Context, id uint32, path string, readOnly bool, imageFormat vm.VPMemImageFormat) error {
	switch uvm.state {
	case vm.StatePreCreated:
		return uvm.addVPMemDevicePreCreated(ctx, id, path, readOnly, imageFormat)
	case vm.StateCreated:
		fallthrough
	case vm.StateRunning:
		return uvm.addVPMemDeviceCreatedRunning(ctx, id, path, readOnly, imageFormat)
	default:
		return fmt.Errorf("VM is not in valid state for this operation: %d", uvm.state)
	}
}

func (uvm *utilityVM) addVPMemDevicePreCreated(ctx context.Context, id uint32, path string, readOnly bool, imageFormat vm.VPMemImageFormat) error {
	if uvm.doc.VirtualMachine.Devices.VirtualPMem == nil {
		return errors.New("VPMem controller has not been added")
	}
	imageFormatString, err := getVPMemImageFormatString(imageFormat)
	if err != nil {
		return err
	}
	uvm.doc.VirtualMachine.Devices.VirtualPMem.Devices[strconv.Itoa(int(id))] = hcsschema.VirtualPMemDevice{
		HostPath:    path,
		ReadOnly:    readOnly,
		ImageFormat: imageFormatString,
	}
	return nil
}

func (uvm *utilityVM) addVPMemDeviceCreatedRunning(ctx context.Context, id uint32, path string, readOnly bool, imageFormat vm.VPMemImageFormat) error {
	imageFormatString, err := getVPMemImageFormatString(imageFormat)
	if err != nil {
		return err
	}
	request := &hcsschema.ModifySettingRequest{
		RequestType: requesttype.Add,
		Settings: hcsschema.VirtualPMemDevice{
			HostPath:    path,
			ReadOnly:    readOnly,
			ImageFormat: imageFormatString,
		},
		ResourcePath: fmt.Sprintf("VirtualMachine/Devices/VirtualPMem/Devices/%d", id),
	}
	return uvm.cs.Modify(ctx, request)
}

func getVPMemImageFormatString(imageFormat vm.VPMemImageFormat) (string, error) {
	switch imageFormat {
	case vm.VPMemImageFormatVHD1:
		return "Vhd1", nil
	case vm.VPMemImageFormatVHDX:
		return "Vhdx", nil
	default:
		return "", fmt.Errorf("unsupported VPMem image format: %d", imageFormat)
	}
}
