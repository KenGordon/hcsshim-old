package hcs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/hcs"
	"github.com/Microsoft/hcsshim/internal/requesttype"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
	"github.com/Microsoft/hcsshim/internal/schemaversion"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/osversion"
)

var (
	errNotInPreCreatedState = errors.New("VM is not in pre-created state")
	errNotInCreatedState    = errors.New("VM is not in created state")
	errNotInRunningState    = errors.New("VM is not in running state")
)

var (
	LCOWSource = lcowSource{}
)

type lcowSource struct{}

func (s lcowSource) NewLinuxUVM(ctx context.Context, id string, owner string) (vm.UVM, error) {
	return &utilityVM{
		id:    id,
		state: vm.StatePreCreated,
		doc: &hcsschema.ComputeSystem{
			Owner:                             owner,
			SchemaVersion:                     schemaversion.SchemaV21(),
			ShouldTerminateOnLastHandleClosed: true,
			VirtualMachine: &hcsschema.VirtualMachine{
				StopOnReset: true,
				Chipset:     &hcsschema.Chipset{},
				ComputeTopology: &hcsschema.Topology{
					Memory: &hcsschema.Memory2{
						AllowOvercommit: true,
					},
					Processor: &hcsschema.Processor2{},
				},
				Devices: &hcsschema.Devices{
					HvSocket: &hcsschema.HvSocket2{
						HvSocketConfig: &hcsschema.HvSocketSystemConfig{
							// Allow administrators and SYSTEM to bind to vsock sockets
							// so that we can create a GCS log socket.
							DefaultBindSecurityDescriptor: "D:P(A;;FA;;;SY)(A;;FA;;;BA)",
						},
					},
					Plan9: &hcsschema.Plan9{},
				},
			},
		},
	}, nil
}

type utilityVM struct {
	id    string
	state vm.State
	doc   *hcsschema.ComputeSystem
	cs    *hcs.System
	vmID  guid.GUID
}

func (uvm *utilityVM) ID() string {
	return uvm.id
}

func (uvm *utilityVM) State() vm.State {
	return uvm.state
}

func (uvm *utilityVM) Create(ctx context.Context) (err error) {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
	}
	cs, err := hcs.CreateComputeSystem(ctx, uvm.id, uvm.doc)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			cs.Terminate(ctx)
			cs.Wait()
		}
	}()

	uvm.cs = cs
	properties, err := cs.Properties(ctx)
	if err != nil {
		return err
	}
	uvm.vmID = properties.RuntimeID

	uvm.state = vm.StateCreated
	return nil
}

func (uvm *utilityVM) Start(ctx context.Context) (err error) {
	if uvm.state != vm.StateCreated {
		return errNotInCreatedState
	}
	if err := uvm.cs.Start(ctx); err != nil {
		return err
	}
	uvm.state = vm.StateRunning
	return nil
}

func (uvm *utilityVM) Stop(ctx context.Context) error {
	if uvm.state != vm.StateRunning {
		return errNotInRunningState
	}
	if err := uvm.cs.Terminate(ctx); err != nil {
		return err
	}
	uvm.state = vm.StateTerminated
	return nil
}

func (uvm *utilityVM) Wait() error {
	if err := uvm.cs.Wait(); err != nil {
		return err
	}
	return nil
}

func (uvm *utilityVM) SetMemoryLimit(ctx context.Context, memoryMB uint64) error {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
	}
	uvm.doc.VirtualMachine.ComputeTopology.Memory.SizeInMB = int32(memoryMB)
	return nil
}

func (uvm *utilityVM) SetMemoryConfig(ctx context.Context, config *vm.MemoryConfig) error {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
	}
	memory := uvm.doc.VirtualMachine.ComputeTopology.Memory
	memory.AllowOvercommit = config.BackingType == vm.MemoryBackingTypeVirtual
	memory.EnableDeferredCommit = config.DeferredCommit
	memory.EnableHotHint = config.HotHint
	memory.EnableColdHint = config.ColdHint
	memory.EnableColdDiscardHint = config.ColdDiscardHint
	return nil
}

func (uvm *utilityVM) SetMMIOConfig(ctx context.Context, lowGapMB uint64, highBaseMB uint64, highGapMB uint64) error {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
	}
	memory := uvm.doc.VirtualMachine.ComputeTopology.Memory
	memory.LowMMIOGapInMB = lowGapMB
	memory.HighMMIOBaseInMB = highBaseMB
	memory.HighMMIOGapInMB = highGapMB
	return nil
}

func (uvm *utilityVM) SetProcessorCount(ctx context.Context, count uint64) error {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
	}
	uvm.doc.VirtualMachine.ComputeTopology.Processor.Count = int32(count)
	return nil
}

func (uvm *utilityVM) AddSCSIController(ctx context.Context, id uint32) error {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
	}
	if uvm.doc.VirtualMachine.Devices.Scsi == nil {
		uvm.doc.VirtualMachine.Devices.Scsi = make(map[string]hcsschema.Scsi, 1)
	}
	uvm.doc.VirtualMachine.Devices.Scsi[strconv.Itoa(int(id))] = hcsschema.Scsi{
		Attachments: make(map[string]hcsschema.Attachment),
	}
	return nil
}

func (uvm *utilityVM) AddSCSIDisk(ctx context.Context, controller uint32, lun uint32, path string, typ vm.SCSIDiskType, readOnly bool) error {
	switch uvm.state {
	case vm.StatePreCreated:
		return uvm.addSCSIDiskPreCreated(ctx, controller, lun, path, typ, readOnly)
	case vm.StateCreated:
		fallthrough
	case vm.StateRunning:
		return uvm.addSCSIDiskCreatedRunning(ctx, controller, lun, path, typ, readOnly)
	default:
		return fmt.Errorf("VM is not in valid state for this operation: %d", uvm.state)
	}
}

func (uvm *utilityVM) addSCSIDiskPreCreated(ctx context.Context, controller uint32, lun uint32, path string, typ vm.SCSIDiskType, readOnly bool) error {
	return errors.New("not implemented")
}

func (uvm *utilityVM) addSCSIDiskCreatedRunning(ctx context.Context, controller uint32, lun uint32, path string, typ vm.SCSIDiskType, readOnly bool) error {
	diskTypeString, err := getSCSIDiskTypeString(typ)
	if err != nil {
		return err
	}
	request := &hcsschema.ModifySettingRequest{
		RequestType: requesttype.Add,
		Settings: hcsschema.Attachment{
			Path:     path,
			Type_:    diskTypeString,
			ReadOnly: readOnly,
		},
		ResourcePath: fmt.Sprintf("VirtualMachine/Devices/Scsi/%d/Attachments/%d", controller, lun),
	}
	if err := uvm.cs.Modify(ctx, request); err != nil {
		return err
	}
	return nil
}

func getSCSIDiskTypeString(typ vm.SCSIDiskType) (string, error) {
	switch typ {
	case vm.SCSIDiskTypeVHD1:
		fallthrough
	case vm.SCSIDiskTypeVHDX:
		return "VirtualDisk", nil
	case vm.SCSIDiskTypePassThrough:
		return "PassThru", nil
	default:
		return "", fmt.Errorf("unsupported SCSI disk type: %d", typ)
	}
}

func (uvm *utilityVM) AddVPMemController(ctx context.Context, maximumDevices uint32, maximumSizeBytes uint64) error {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
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
	if err := uvm.cs.Modify(ctx, request); err != nil {
		return err
	}
	return nil
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

func (uvm *utilityVM) SetUEFIBoot(ctx context.Context, dir string, path string, args string) error {
	if uvm.state != vm.StatePreCreated {
		return errNotInPreCreatedState
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
		return errNotInPreCreatedState
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

func (uvm *utilityVM) HVSocketListen(ctx context.Context, serviceID guid.GUID) (net.Listener, error) {
	l, err := winio.ListenHvsock(&winio.HvsockAddr{
		VMID:      uvm.vmID,
		ServiceID: serviceID,
	})
	if err != nil {
		return nil, err
	}
	return l, nil
}

func (uvm *utilityVM) AddNIC(ctx context.Context, nicID guid.GUID, endpointID string, mac string) error {
	request := hcsschema.ModifySettingRequest{
		RequestType:  requesttype.Add,
		ResourcePath: fmt.Sprintf("VirtualMachine/Devices/NetworkAdapters/%s", nicID.String()),
		Settings: hcsschema.NetworkAdapter{
			EndpointId: endpointID,
			MacAddress: mac,
		},
	}
	if err := uvm.cs.Modify(ctx, request); err != nil {
		return err
	}
	return nil
}
