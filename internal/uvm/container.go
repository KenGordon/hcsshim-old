package uvm

import (
	"context"
	"errors"

	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/hcs/schema1"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
)

type hcsVMContainer struct {
	*UtilityVM
	waitBlock chan struct{}
}

var _ cow.Container = &hcsVMContainer{}

func NewVMContainer(uvm *UtilityVM) *hcsVMContainer {
	return &hcsVMContainer{
		UtilityVM: uvm,
		waitBlock: make(chan struct{}),
	}
}

func (vc *hcsVMContainer) Shutdown(ctx context.Context) error {
	return vc.hcsSystem.Terminate(ctx)
}

// Modify
//
// TODO: check which modifications HCS allows and how that works
func (vc *hcsVMContainer) Modify(ctx context.Context, config interface{}) error {
	return errors.New("not implemented")
}

func (vc *hcsVMContainer) Properties(ctx context.Context, types ...schema1.PropertyType) (*schema1.ContainerProperties,
	error) {
	return vc.UtilityVM.hcsSystem.Properties(ctx, types...)
}

// PropertiesV2
//
// TODO: in the case of VM this should probably return properties of the UVM?
func (vc *hcsVMContainer) PropertiesV2(ctx context.Context, types ...hcsschema.PropertyType) (*hcsschema.Properties, error) {
	return vc.UtilityVM.hcsSystem.PropertiesV2(ctx, types...)
}

// WaitChannel
//
// TODO: should this be hooked up with vc.hcsSystem.Terminate call?
func (vc *hcsVMContainer) WaitChannel() <-chan struct{} {
	return vc.waitBlock
}

// WaitError
//
// TODO: should this return terminate call error?
func (vc *hcsVMContainer) WaitError() error {
	return errors.New("not implemented")
}
