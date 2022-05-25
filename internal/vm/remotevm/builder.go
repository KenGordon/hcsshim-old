package remotevm

import (
	"context"
	"fmt"
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
	"github.com/sirupsen/logrus"
)

var _ vm.UVMBuilder = &utilityVMBuilder{}

type utilityVMBuilder struct {
	id, binPath, addr, owner string
	guestOS                  vm.GuestOS
	ignoreSupported          bool
	networkNS                string
	config                   *vmservice.VMConfig
}

func NewUVMBuilder(ctx context.Context, id, owner, binPath, addr string, guestOS vm.GuestOS) (_ vm.UVMBuilder, err error) {
	log.G(ctx).WithFields(logrus.Fields{
		"binary":  binPath,
		"address": addr,
	}).Debug("creating RemoteVM builder")

	return &utilityVMBuilder{
		id:      id,
		addr:    addr,
		owner:   owner,
		binPath: binPath,
		guestOS: guestOS,
		config: &vmservice.VMConfig{
			MemoryConfig:    &vmservice.MemoryConfig{},
			DevicesConfig:   &vmservice.DevicesConfig{},
			ProcessorConfig: &vmservice.ProcessorConfig{},
			SerialConfig:    &vmservice.SerialConfig{},
			ExtraData:       make(map[string]string),
		},
	}, nil
}

func (uvmb *utilityVMBuilder) Create(ctx context.Context, opts []vm.CreateOpt) (_ vm.UVM, err error) {
	// Apply any opts
	for _, o := range opts {
		if err := o(ctx, uvmb); err != nil {
			return nil, fmt.Errorf("failed applying create options for Utility VM: %w", err)
		}
	}

	var cmd *exec.Cmd
	if uvmb.binPath != "" {
		log.G(ctx).WithFields(logrus.Fields{
			"binary":  uvmb.binPath,
			"address": uvmb.addr,
		}).Debug("starting remotevm server process")

		// If no address passed, just generate a random one.
		if uvmb.addr == "" {
			uvmb.addr, err = randomUnixSockAddr()
			if err != nil {
				return nil, err
			}
		}

		// This requires the VMM to have a --ttrpc flag, or already be running and serving
		// on the addr.
		cmd = exec.Command(uvmb.binPath, "--ttrpc", uvmb.addr)
		cmd.Stderr = os.Stderr
		p, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		if err := uvmb.startVMM(cmd); err != nil {
			return nil, err
		}

		// Wait for stdout to close. This is our signal that the server is successfully up and running.
		_, _ = io.Copy(ioutil.Discard, p)
	}

	conn, err := net.Dial("unix", uvmb.addr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial remotevm address %q: %w", uvmb.addr, err)
	}

	c := ttrpc.NewClient(conn, ttrpc.WithOnClose(func() { conn.Close() }))
	vmClient := vmservice.NewVMClient(c)

	var capabilities *vmservice.CapabilitiesVMResponse
	if !uvmb.ignoreSupported {
		// Grab what capabilities the virtstack supports up front.
		capabilities, err = vmClient.CapabilitiesVM(ctx, &ptypes.Empty{})
		if err != nil {
			return nil, fmt.Errorf("failed to get virtstack capabilities from vmservice: %w", err)
		}
	}

	_, err = vmClient.CreateVM(ctx, &vmservice.CreateVMRequest{Config: uvmb.config, LogID: uvmb.id})
	if err != nil {
		return nil, fmt.Errorf("failed to create remote VM: %w", err)
	}

	log.G(ctx).WithFields(logrus.Fields{
		logfields.UVMID:         uvmb.id,
		"vmservice-address":     uvmb.addr,
		"vmservice-binary-path": uvmb.binPath,
	}).Debug("created utility VM")

	uvm := &utilityVM{
		id:              uvmb.id,
		vmmProc:         cmd,
		waitBlock:       make(chan struct{}),
		ignoreSupported: uvmb.ignoreSupported,
		config:          uvmb.config,
		client:          vmClient,
		capabilities:    capabilities,
	}

	go uvm.waitBackground()
	return uvm, nil
}
