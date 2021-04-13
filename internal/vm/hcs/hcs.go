package hcs

import (
	"context"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/hcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
	"github.com/Microsoft/hcsshim/internal/schemaversion"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/pkg/errors"
)

type hcsSource struct{}

func NewSource() vm.UVMSource {
	return &hcsSource{}
}

func (s *hcsSource) NewLinuxUVM(ctx context.Context, id, owner string) (vm.UVM, error) {
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

// Stub for now
func (s *hcsSource) NewWindowsUVM(ctx context.Context, id, owner string) (vm.UVM, error) {
	return nil, vm.ErrNotSupported
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
		return vm.ErrNotInPreCreatedState
	}
	cs, err := hcs.CreateComputeSystem(ctx, uvm.id, uvm.doc)
	if err != nil {
		return errors.Wrap(err, "failed to create hcs compute system")
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
		return vm.ErrNotInCreatedState
	}
	if err := uvm.cs.Start(ctx); err != nil {
		return err
	}
	uvm.state = vm.StateRunning
	return nil
}

func (uvm *utilityVM) Stop(ctx context.Context) error {
	if uvm.state != vm.StateRunning {
		return vm.ErrNotInRunningState
	}
	if err := uvm.cs.Terminate(ctx); err != nil {
		return err
	}
	uvm.state = vm.StateTerminated
	return nil
}

func (uvm *utilityVM) Wait() error {
	return uvm.cs.Wait()
}
