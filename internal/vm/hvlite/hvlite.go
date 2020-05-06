package hvlite

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/vm"
)

var (
	LCOWSource = lcowSource{}
)

type lcowSource struct{}

func (s lcowSource) NewLinuxUVM(id string, owner string) (vm.UVM, error) {
	// Launch hvlite control process with named pipe path.
	// Connect to named pipe and use it for hvlite GRPC interface.
	// Return wrapper type containing the GRPC client.
	return &utilityVM{
		id: id,
		config: &VMConfig{
			VmID: id,
		},
		client: nil,
	}, nil
}

type utilityVM struct {
	id     string
	state  vm.State
	config *VMConfig
	client VMClient
}

func (uvm *utilityVM) ID() string {
	return uvm.id
}

func (uvm *utilityVM) State() vm.State {
	return uvm.state
}

func (uvm *utilityVM) Create(ctx context.Context) error {
	if _, err := uvm.client.CreateVM(ctx, &CreateVMRequest{Config: uvm.config}); err != nil {
		return fmt.Errorf("failed to create VM: %s", err)
	}
	uvm.state = vm.StateCreated
	return nil
}

func (uvm *utilityVM) Start(ctx context.Context) error {
	uvm.state = vm.StateRunning
	return nil
}

func (uvm *utilityVM) Stop(ctx context.Context) error {
	uvm.state = vm.StateTerminated
	return nil
}

func (uvm *utilityVM) Wait() error {
	return nil
}

func (uvm *utilityVM) AddSCSIController(ctx context.Context, id uint32) error {
	return nil
}

func (uvm *utilityVM) AddSCSIDisk(ctx context.Context, controller uint32, lun uint32, path string, typ vm.SCSIDiskType, readOnly bool) error {
	var diskType SCSIDisk_DiskType
	switch typ {
	case vm.SCSIDiskTypeVHD1:
		diskType = SCSIDisk_SCSI_DISK_TYPE_VHD1
	case vm.SCSIDiskTypeVHDX:
		diskType = SCSIDisk_SCSI_DISK_TYPE_VHDX
	default:
		return fmt.Errorf("unsupported SCSI disk type: %d", typ)
	}
	disk := &SCSIDisk{
		Controller: controller,
		Lun:        lun,
		Path:       path,
		Type:       diskType,
		ReadOnly:   readOnly,
	}
	if uvm.state == vm.StateCreated {
		uvm.config.ScsiDisks = append(uvm.config.ScsiDisks, disk)
	} else if uvm.state == vm.StateRunning {
		if _, err := uvm.client.AddSCSIDisk(ctx, &AddSCSIDiskRequest{Disk: disk}); err != nil {
			return fmt.Errorf("failed to add SCSI disk: %s", err)
		}
	} else {
		return errors.New("VM is not in created or running state")
	}
	return nil
}

func (uvm *utilityVM) HVSocketListen(ctx context.Context, serviceID guid.GUID) (net.Listener, error) {
	if uvm.state != vm.StateRunning {
		return nil, errors.New("VM is not in running state")
	}
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for unix socket: %s", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %s", err)
	}
	if err := os.Remove(f.Name()); err != nil {
		return nil, fmt.Errorf("failed to delete temp file to free up name: %s", err)
	}
	l, err := net.Listen("unix", f.Name())
	if _, err := uvm.client.HVSocketListen(ctx, &HVSocketListenRequest{
		ServiceID:    serviceID.String(),
		ListenerPath: f.Name(),
	}); err != nil {
		return nil, fmt.Errorf("failed to get HVSocket listener: %s", err)
	}
	return l, nil
}

func (uvm *utilityVM) AddNIC(ctx context.Context, nicID guid.GUID, endpointID string, mac string) error {
	return nil
}
