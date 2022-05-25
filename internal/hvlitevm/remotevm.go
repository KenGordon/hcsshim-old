package hvlitevm

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/Microsoft/hcsshim/internal/gcs/transport"
	"github.com/Microsoft/hcsshim/internal/guestpath"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/logfields"
	"github.com/Microsoft/hcsshim/internal/ospath"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vm/remotevm"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sync/errgroup"
)

type UtilityVM struct {
	ID              string
	System          vm.UVM
	GuestConnection *gcs.GuestConnection

	//private values
	outputProcessingDone chan struct{}
	udsPath              string
}

type Options struct {
	ID    string
	Owner string

	// KernelFile specifies path to the kernel image to boot from
	KernelFile string
	// InitrdPath specifies path of the initrd to use as the rootfs
	InitrdPath string
	// BinPath is the path to the binary implementing the vmservice interface
	BinPath string

	TTRPCAddress string
}

func Create(ctx context.Context, opts *Options) (*UtilityVM, error) {
	builder, err := remotevm.NewUVMBuilder(
		ctx,
		opts.ID,
		opts.Owner,
		opts.BinPath,
		opts.TTRPCAddress,
		vm.Linux,
	)
	if err != nil {
		return nil, err
	}

	kernelArgs := `8250_core.nr_uarts=1 8250_core.skip_txen_test=1 console=ttyS0,115200 pci=off brd.rd_nr=0 pmtmr=0 -- -e 1 /bin/vsockexec -e 109 /bin/gcs -v4 -log-format json -disable-time-sync -loglevel debug`
	boot := builder.(vm.BootManager)
	if err := boot.SetLinuxKernelDirectBoot(
		opts.KernelFile,
		opts.InitrdPath,
		kernelArgs,
	); err != nil {
		return nil, fmt.Errorf("failed to set Linux kernel direct boot: %w", err)
	}

	proc := builder.(vm.ProcessorManager)
	if err := proc.SetProcessorCount(2); err != nil {
		return nil, err
	}

	mem := builder.(vm.MemoryManager)
	if err := mem.SetMemoryLimit(ctx, 2048); err != nil {
		return nil, err
	}

	scsi, ok := builder.(vm.SCSIManager)
	if !ok {
		return nil, fmt.Errorf("stopping SCSI setup: %w", vm.ErrNotSupported)
	}
	if err := scsi.AddSCSIController(0); err != nil {
		return nil, fmt.Errorf("failed to add scsi controller: %w", err)
	}

	vmsock := builder.(vm.HybridVMSocketManager)
	udsPath, err := randomUnixSockAddr()
	if err != nil {
		return nil, err
	}
	vmsock.SetVMSockRelay(udsPath)

	vmCreateOpts := []vm.CreateOpt{remotevm.WithIgnoreSupported()}
	system, err := builder.Create(ctx, vmCreateOpts)
	if err != nil {
		return nil, err
	}
	return &UtilityVM{
		System:               system,
		udsPath:              udsPath,
		outputProcessingDone: make(chan struct{}),
	}, nil
}

func (uvm *UtilityVM) Start(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	g, gctx := errgroup.WithContext(ctx)
	defer func() {
		_ = g.Wait()
	}()
	defer cancel()

	log.G(ctx).Infof("Creating entropy listener on port %d", entropyListenerPort)
	entropyListener, err := listenHybridVsock(uvm.udsPath, entropyListenerPort)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Creating output listener on port %d", logOutputListenerPort)
	outputListener, err := listenHybridVsock(uvm.udsPath, logOutputListenerPort)
	if err != nil {
		return err
	}

	// Prepare to provide entropy to the init process in the background. This
	// must be done in a goroutine since, when using the internal bridge, the
	// call to Start() will block until the GCS launches, and this cannot occur
	// until the host accepts and closes the entropy connection.
	g.Go(func() error {
		conn, err := acceptAndClose(gctx, entropyListener)
		entropyListener = nil
		if err != nil {
			return fmt.Errorf("failed to connect to entropy socket: %s", err)
		}
		defer conn.Close()

		log.G(ctx).Info("Accepted entropy socket connection")
		_, err = io.CopyN(conn, rand.Reader, 512)
		if err != nil {
			return fmt.Errorf("failed to write entropy: %s", err)
		}
		log.G(ctx).Info("Entropy socket finished piping bytes")
		return nil
	})

	g.Go(func() error {
		conn, err := acceptAndClose(gctx, outputListener)
		outputListener = nil
		if err != nil {
			close(uvm.outputProcessingDone)
			return fmt.Errorf("failed to connect to log socket: %s", err)
		}

		log.G(ctx).Info("Accepted log output connection")
		go func() {
			outputHandler(conn)
			close(uvm.outputProcessingDone)
		}()
		return nil
	})

	if err := uvm.System.Start(ctx); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			uvm.Close()
		}
	}()

	// Collect any errors from writing entropy or establishing the log
	// connection.
	log.G(ctx).Info("Waiting on vsock conns")
	if err = g.Wait(); err != nil {
		return err
	}

	log.G(ctx).Infof("Creating GCS listener on port %d", transport.LinuxGcsVsockPort)
	gcListener, err := listenHybridVsock(uvm.udsPath, transport.LinuxGcsVsockPort)
	if err != nil {
		return err
	}

	// Accept the GCS connection.
	conn, err := acceptAndClose(ctx, gcListener)
	gcListener = nil
	if err != nil {
		return fmt.Errorf("failed to connect to GCS: %w", err)
	}
	log.G(ctx).Info("Accepted GCS connection")

	var initGuestState *gcs.InitialGuestState
	// Start the GCS protocol.
	gcc := &gcs.GuestConnectionConfig{
		Conn: conn,
		Log:  log.G(ctx).WithField(logfields.UVMID, uvm.ID),
		IoListen: func(port uint32) (net.Listener, error) {
			return listenHybridVsock(uvm.udsPath, port)
		},
		InitGuestState: initGuestState,
	}
	guestConnection, err := gcc.Connect(ctx, true)
	if err != nil {
		return err
	}

	uvm.GuestConnection = guestConnection
	return nil
}

func (uvm *UtilityVM) Wait() error {
	err := uvm.System.Wait()

	if uvm.outputProcessingDone != nil {
		<-uvm.outputProcessingDone
	}
	return err
}

func (uvm *UtilityVM) Stop(ctx context.Context) error {
	return uvm.System.Stop(ctx)
}

func (uvm *UtilityVM) Close() error {
	return uvm.System.Close()
}

// TODO katiewasnothere: this should go in centralized location
type linuxHostedSystem struct {
	SchemaVersion    *hcsschema.Version
	OciBundlePath    string
	OciSpecification *specs.Spec
}

func (uvm *UtilityVM) CreateContainer(ctx context.Context, id string, spec *specs.Spec) (cow.Container, error) {
	if uvm.GuestConnection == nil {
		return nil, fmt.Errorf("cannot create a container %s, guest connection is nil", id)
	}

	// setup stuff
	scsi, ok := uvm.System.(vm.SCSIManager)
	if !ok {
		return nil, fmt.Errorf("cannot mount layers as scsi: %s", vm.ErrNotSupported)
	}

	// defer remove scsi files
	uvmLayerPaths := []string{}
	imageLayerPaths := spec.Windows.LayerFolders[:len(spec.Windows.LayerFolders)-1]
	for i, layerPrefix := range imageLayerPaths {
		guestReq := guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
			RequestType:  guestrequest.RequestTypeAdd,
		}
		uvmPath := fmt.Sprintf(guestpath.LCOWGlobalMountPrefixFmt, i)
		// TODO katiewasnothere: IMPORTANT: use correct index for mount path
		guestReq.Settings = guestresource.LCOWMappedVirtualDisk{
			MountPath:  uvmPath,
			Lun:        uint8(i),
			Controller: 0,
		}

		uvmLayerPaths = append(uvmLayerPaths, uvmPath)
		layerPath := filepath.Join(layerPrefix, "layer.vhd")
		if err := scsi.AddSCSIDisk(ctx, 0, uint32(i), layerPath, vm.SCSIDiskTypeVHD1, false); err != nil {
			return nil, fmt.Errorf("failed to add SCSI disk: %w", err)
		}
		if err := uvm.GuestConnection.Modify(ctx, guestReq); err != nil {
			return nil, fmt.Errorf("failed to make guest request to add scsi disk %v: %w", guestReq, err)
		}
	}

	// Add in the scratch path as scsi
	scratchPath := filepath.Join(spec.Windows.LayerFolders[len(spec.Windows.LayerFolders)-1], "sandbox.img")
	guestReq := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeAdd,
	}

	uvmScratchPath := fmt.Sprintf(guestpath.LCOWGlobalMountPrefixFmt, len(spec.Windows.LayerFolders))
	// TODO katiewasnothere: IMPORTANT: use correct index for mount path
	guestReq.Settings = guestresource.LCOWMappedVirtualDisk{
		MountPath:  fmt.Sprintf(guestpath.LCOWGlobalMountPrefixFmt, len(spec.Windows.LayerFolders)),
		Lun:        uint8(len(spec.Windows.LayerFolders)),
		Controller: 0,
	}

	if err := scsi.AddSCSIDisk(ctx, 0, uint32(len(spec.Windows.LayerFolders)), scratchPath, vm.SCSIDiskTypeVHD1, false); err != nil {
		return nil, fmt.Errorf("failed to add SCSI disk: %w", err)
	}
	if err := uvm.GuestConnection.Modify(ctx, guestReq); err != nil {
		return nil, fmt.Errorf("failed to make guest request to add scsi disk: %w", err)
	}

	// katiewasnothere: Mount as contaienr layer? Do we need to combine them? yes right?
	lcolRootInUVM := fmt.Sprintf(guestpath.LCOWRootPrefixInUVM+"/%s", id)

	layers := []hcsschema.Layer{}
	for _, l := range uvmLayerPaths {
		layers = append(layers, hcsschema.Layer{Path: l})
	}

	rootfs := ospath.Join("linux", lcolRootInUVM, guestpath.RootfsPath)
	guestReq = guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeCombinedLayers,
		RequestType:  guestrequest.RequestTypeAdd,
		Settings: guestresource.LCOWCombinedLayers{
			ContainerRootPath: rootfs,
			Layers:            layers,
			ScratchPath:       uvmScratchPath,
		},
	}
	if err := uvm.GuestConnection.Modify(ctx, guestReq); err != nil {
		return nil, fmt.Errorf("failed to make guest request to combine layers %v: %w", guestReq, err)
	}

	// update root path in spec
	spec.Root.Path = rootfs

	containerSettings := &linuxHostedSystem{
		SchemaVersion:    &hcsschema.Version{Major: 2, Minor: 1},
		OciBundlePath:    lcolRootInUVM,
		OciSpecification: spec,
	}

	c, err := uvm.GuestConnection.CreateContainer(ctx, id, containerSettings)
	if err != nil {
		return nil, fmt.Errorf("failed to create container %s: %s", id, err)
	}
	return c, nil
}
