package remotevm

import (
	"context"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/logfields"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vmservice"
	"github.com/containerd/ttrpc"
	ptypes "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var _ vm.UVMBuilder = &utilityVMBuilder{}

type utilityVMBuilder struct {
	id, binpath, addr string
	guestOS           vm.GuestOS
	ignoreSupported   bool
	config            *vmservice.VMConfig
	client            vmservice.VMService
}

func NewUVMBuilder(ctx context.Context, id, owner, binPath, addr string, guestOS vm.GuestOS) (_ vm.UVMBuilder, err error) {
	if binPath != "" {
		log.G(ctx).WithFields(logrus.Fields{
			"binary":  binPath,
			"address": addr,
		}).Debug("starting remotevm server process")

		// If no address passed, just generate a random one.
		if addr == "" {
			addr, err = randomUnixSockAddr()
			if err != nil {
				return nil, err
			}
		}

		cmd := exec.Command(binPath, "--ttrpc", addr)
		cmd.Stderr = os.Stderr
		p, err := cmd.StdoutPipe()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create stdout pipe")
		}

		if err := cmd.Start(); err != nil {
			return nil, errors.Wrap(err, "failed to start remotevm server process")
		}

		// Wait for stdout to close. This is our signal that the server is successfully up and running.
		_, _ = io.Copy(ioutil.Discard, p)
	}

	conn, err := net.Dial("unix", addr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial remotevm address %q", addr)
	}

	c := ttrpc.NewClient(conn, ttrpc.WithOnClose(func() { conn.Close() }))
	vmClient := vmservice.NewVMClient(c)

	return &utilityVMBuilder{
		id:      id,
		guestOS: guestOS,
		config: &vmservice.VMConfig{
			MemoryConfig:    &vmservice.MemoryConfig{},
			DevicesConfig:   &vmservice.DevicesConfig{},
			ProcessorConfig: &vmservice.ProcessorConfig{},
			SerialConfig:    &vmservice.SerialConfig{},
			ExtraData:       make(map[string]string),
		},
		client: vmClient,
	}, nil
}

func (uvmb *utilityVMBuilder) Create(ctx context.Context, opts []vm.CreateOpt) (_ vm.UVM, err error) {
	// Apply any opts
	for _, o := range opts {
		if err := o(ctx, uvmb); err != nil {
			return nil, errors.Wrap(err, "failed applying create options for Utility VM")
		}
	}

	var capabilities *vmservice.CapabilitiesVMResponse
	if !uvmb.ignoreSupported {
		// Grab what capabilities the virtstack supports up front.
		capabilities, err = uvmb.client.CapabilitiesVM(ctx, &ptypes.Empty{})
		if err != nil {
			return nil, errors.Wrap(err, "failed to get virtstack capabilities from vmservice")
		}
	}

	_, err = uvmb.client.CreateVM(ctx, &vmservice.CreateVMRequest{Config: uvmb.config, LogID: uvmb.id})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create remote VM")
	}

	log.G(ctx).WithFields(logrus.Fields{
		logfields.UVMID:         uvmb.id,
		"vmservice-address":     uvmb.addr,
		"vmservice-binary-path": uvmb.binpath,
	}).Debug("created utility VM")

	uvm := &utilityVM{
		id:              uvmb.id,
		waitBlock:       make(chan struct{}),
		ignoreSupported: uvmb.ignoreSupported,
		config:          uvmb.config,
		client:          uvmb.client,
		capabilities:    capabilities,
	}

	go uvm.waitBackground()
	return uvm, nil
}
