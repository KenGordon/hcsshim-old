package remotevm

import (
	"context"
	"io/ioutil"
	"net"
	"os"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vmservice"
	"github.com/pkg/errors"
)

func (uvm *remoteVM) HVSocketListen(ctx context.Context, serviceID guid.GUID) (net.Listener, error) {
	if uvm.state != vm.StateCreated && uvm.state != vm.StateRunning {
		return nil, errors.New("VM is not in created or running state")
	}

	f, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create temp file for unix socket")
	}

	if err := f.Close(); err != nil {
		return nil, errors.Wrap(err, "failed to close temp file")
	}

	if err := os.Remove(f.Name()); err != nil {
		return nil, errors.Wrap(err, "failed to delete temp file to free up name")
	}

	l, err := net.Listen("unix", f.Name())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen on unix socket %q", f.Name())
	}

	if _, err := uvm.client.VMSocket(ctx, &vmservice.VMSocketRequest{
		Type: vmservice.ModifyType_ADD,
		Config: &vmservice.VMSocketRequest_HvsocketList{
			HvsocketList: &vmservice.HVSocketListen{
				ServiceID:    serviceID.String(),
				ListenerPath: f.Name(),
			},
		},
	}); err != nil {
		return nil, errors.Wrap(err, "failed to get HVSocket listener")
	}
	return l, nil
}

func (uvm *remoteVM) VSockListen(ctx context.Context, port uint32) (net.Listener, error) {
	return nil, vm.ErrNotSupported
}
