package hvlite

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type source struct {
	binary         string
	port           uint16
	createInstance bool
}

func NewSource(binary string, port uint16, createInstance bool) (vm.UVMSource, error) {
	return &source{
		binary:         binary,
		port:           port,
		createInstance: createInstance,
	}, nil
}

func (s *source) NewLinuxUVM(ctx context.Context, id string, owner string) (vm.UVM, error) {
	address := fmt.Sprintf("127.0.0.1:%d", s.port)

	if s.createInstance {
		log.G(ctx).WithFields(logrus.Fields{
			"binary":  s.binary,
			"address": address,
		}).Debug("starting hvlite control process")
		cmd := exec.Command(s.binary, "--grpc", address)
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start hvlite control process: %s", err)
		}
	}

	c, err := grpc.DialContext(ctx, address, grpc.WithInsecure(), grpc.WithBlock(), grpc.WithTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to dial hvlite control process: %s", err)
	}
	return &utilityVM{
		id: id,
		config: &VMConfig{
			VmID: id,
		},
		client: NewVMClient(c),
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
	if _, err := uvm.client.StartVM(ctx, &StartVMRequest{}); err != nil {
		return fmt.Errorf("failed to start VM: %s", err)
	}
	uvm.state = vm.StateRunning
	return nil
}

func (uvm *utilityVM) Stop(ctx context.Context) error {
	if _, err := uvm.client.StopVM(ctx, &StopVMRequest{}); err != nil {
		return fmt.Errorf("failed to stop VM: %s", err)
	}
	uvm.state = vm.StateTerminated
	return nil
}

func (uvm *utilityVM) Wait() error {
	if _, err := uvm.client.WaitVM(context.Background(), &WaitVMRequest{}); err != nil {
		return fmt.Errorf("failed to wait VM: %s", err)
	}
	return nil
}

func (uvm *utilityVM) SetMemoryLimit(ctx context.Context, memoryMB uint64) error {
	if uvm.state != vm.StatePreCreated {
		return errors.New("VM is not in pre-created state")
	}
	uvm.config.MemoryMb = memoryMB
	return nil
}

func (uvm *utilityVM) SetProcessorCount(ctx context.Context, count uint64) error {
	if uvm.state != vm.StatePreCreated {
		return errors.New("VM is not in pre-created state")
	}
	uvm.config.ProcessorCount = count
	return nil
}

func (uvm *utilityVM) SetLinuxKernelDirectBoot(ctx context.Context, kernel string, initRD string, cmd string) error {
	if uvm.state != vm.StatePreCreated {
		return errors.New("VM is not in pre-created state")
	}
	uvm.config.KernelPath = kernel
	uvm.config.InitrdPath = initRD
	uvm.config.KernelArgs = cmd
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
	if uvm.state == vm.StatePreCreated {
		uvm.config.ScsiDisks = append(uvm.config.ScsiDisks, disk)
	} else if uvm.state == vm.StateRunning {
		if _, err := uvm.client.AddSCSIDisk(ctx, &AddSCSIDiskRequest{Disk: disk}); err != nil {
			return fmt.Errorf("failed to add SCSI disk: %s", err)
		}
	} else {
		return errors.New("VM is not in pre-created or running state")
	}
	return nil
}

func (uvm *utilityVM) HVSocketListen(ctx context.Context, serviceID guid.GUID) (net.Listener, error) {
	if uvm.state != vm.StateCreated && uvm.state != vm.StateRunning {
		return nil, errors.New("VM is not in created or running state")
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
