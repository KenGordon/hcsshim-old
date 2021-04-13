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

func (uvm *utilityVM) AddSCSIController(ctx context.Context, id uint32) error {
	if uvm.state != vm.StatePreCreated {
		return vm.ErrNotInPreCreatedState
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
