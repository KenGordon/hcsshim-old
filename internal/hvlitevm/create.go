package hvlitevm

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/Microsoft/hcsshim/internal/gcs/transport"
	"github.com/Microsoft/hcsshim/internal/guestpath"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/logfields"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vm/remotevm"
	"github.com/containerd/console"
	"golang.org/x/sync/errgroup"
)

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

func (uvm *UtilityVM) Create(ctx context.Context, opts *Options) error {
	builder, err := remotevm.NewUVMBuilder(
		ctx,
		opts.ID,
		opts.Owner,
		opts.BinPath,
		opts.TTRPCAddress,
		vm.Linux,
	)
	if err != nil {
		return err
	}

	kernelArgs := `8250_core.nr_uarts=1 8250_core.skip_txen_test=1 console=ttyS0,115200 pci=off brd.rd_nr=0 pmtmr=0 -- -e 1 /bin/vsockexec -e 109 /bin/gcs -v4 -log-format json -disable-time-sync -loglevel debug`
	boot := builder.(vm.BootManager)
	if err := boot.SetLinuxKernelDirectBoot(
		opts.KernelFile,
		opts.InitrdPath,
		kernelArgs,
	); err != nil {
		return fmt.Errorf("failed to set Linux kernel direct boot: %w", err)
	}

	proc := builder.(vm.ProcessorManager)
	if err := proc.SetProcessorCount(2); err != nil {
		return err
	}

	mem := builder.(vm.MemoryManager)
	if err := mem.SetMemoryLimit(ctx, 2048); err != nil {
		return err
	}

	scsi, ok := builder.(vm.SCSIManager)
	if !ok {
		return fmt.Errorf("stopping SCSI setup: %w", vm.ErrNotSupported)
	}
	if err := scsi.AddSCSIController(0); err != nil {
		return fmt.Errorf("failed to add scsi controller: %w", err)
	}

	vmsock := builder.(vm.HybridVMSocketManager)
	udsPath, err := randomUnixSockAddr()
	if err != nil {
		return err
	}
	vmsock.SetVMSockRelay(udsPath)

	opts := []vm.CreateOpt{remotevm.WithIgnoreSupported()}
	remoteVM, err := builder.Create(ctx, opts)
	if err != nil {
		return err
	}

	gc, err := vmStart(ctx, remoteVM, udsPath)
	if err != nil {
		return err
	}

	errCh := make(chan error)
	go func() {
		log.G(ctx).Infof("Waiting on VM %s", remoteVM.ID())
		if err := remoteVM.Wait(); err != nil {
			errCh <- err
		}
	}()

	log.G(ctx).Infof("Protocol in use: %d", gc.Protocol())

	if blockDevs := clictx.StringSlice(blockDev); blockDevs != nil {
		scsi, ok := remoteVM.(vm.SCSIManager)
		if !ok {
			return vm.ErrNotSupported
		}
		for i, blockDev := range blockDevs {
			guestReq := guestrequest.ModificationRequest{
				ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
				RequestType:  guestrequest.RequestTypeAdd,
			}

			guestReq.Settings = guestresource.LCOWMappedVirtualDisk{
				MountPath:  fmt.Sprintf(guestpath.LCOWGlobalMountPrefixFmt, i),
				Lun:        uint8(i),
				Controller: 0,
			}

			if err := scsi.AddSCSIDisk(ctx, 0, uint32(i), blockDev, vm.SCSIDiskTypeVHD1, false); err != nil {
				return fmt.Errorf("failed to add SCSI disk: %w", err)
			}
			if err := gc.Modify(ctx, guestReq); err != nil {
				return fmt.Errorf("failed to make guest request to add scsi disk: %w", err)
			}
		}
	}

	lpp := &lcowProcessParameters{
		ProcessParameters: hcsschema.ProcessParameters{
			CreateStdInPipe:  true,
			CreateStdOutPipe: true,
			CreateStdErrPipe: true,
			EmulateConsole:   true,
			CommandArgs:      []string{"sh"},
			Environment:      map[string]string{"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		},
	}
	p, err := gc.CreateProcess(ctx, lpp)
	if err != nil {
		return err
	}

	// Enable raw mode on the client's console.
	con, err := console.ConsoleFromFile(os.Stdin)
	if err != nil {
		return err
	}
	err = con.SetRaw()
	if err != nil {
		return err
	}
	defer func() {
		_ = con.Reset()
	}()

	// Console reads return EOF whenever the user presses Ctrl-Z.
	// Wrap the reads to translate these EOFs back.
	osStdin := rawConReader{os.Stdin}

	stdin, stdout, stderr := p.Stdio()

	stdioChan := make(chan error, 1)
	logEntry := log.G(ctx)
	go func() {
		n, err := relayIO(os.Stdout, stdout, logEntry, "stdout")
		if err != nil {
			logEntry.WithError(err).Warn("piping stdout failed")
		}
		stdioChan <- err
		logEntry.Infof("finished piping %d bytes from stdout", n)
	}()

	go func() {
		n, err := relayIO(os.Stdout, stderr, logEntry, "stderr")
		if err != nil {
			logEntry.WithError(err).Warn("piping stderr failed")
		}
		stdioChan <- err
		logEntry.Infof("finished piping %d bytes from stderr", n)
	}()

	go func() {
		n, err := relayIO(stdin, osStdin, logEntry, "stdin")
		if err != nil {
			logEntry.WithError(err).Warn("piping stdin failed")
		}
		stdioChan <- err
		logEntry.Infof("finished piping %d bytes from stdin", n)
	}()

	select {
	case err := <-stdioChan:
		log.G(ctx).WithError(err).Info("Stdio relay ended")
		if err := remoteVM.Close(); err != nil {
			return err
		}
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	return remoteVM.ExitError()
}

func (uvm *UtilityVM) Start() {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	g, gctx := errgroup.WithContext(ctx)
	defer func() {
		_ = g.Wait()
	}()
	defer cancel()

	log.G(ctx).Infof("Creating entropy listener on port %d", entropyListenerPort)
	entropyListener, err := listenHybridVsock(udsPrefix, entropyListenerPort)
	if err != nil {
		return nil, err
	}

	log.G(ctx).Infof("Creating output listener on port %d", logOutputListenerPort)
	outputListener, err := listenHybridVsock(udsPrefix, 109)
	if err != nil {
		return nil, err
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
			return fmt.Errorf("failed to connect to log socket: %s", err)
		}

		log.G(ctx).Info("Accepted log output connection")
		go func() {
			outputHandler(conn)
		}()
		return nil
	})

	if err := remoteVM.Start(context.Background()); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			remoteVM.Close()
		}
	}()

	// Collect any errors from writing entropy or establishing the log
	// connection.
	log.G(ctx).Info("Waiting on vsock conns")
	if err = g.Wait(); err != nil {
		return nil, err
	}

	log.G(ctx).Infof("Creating GCS listener on port %d", transport.LinuxGcsVsockPort)
	gcListener, err := listenHybridVsock(udsPrefix, transport.LinuxGcsVsockPort)
	if err != nil {
		return nil, err
	}

	// Accept the GCS connection.
	conn, err := acceptAndClose(ctx, gcListener)
	gcListener = nil
	if err != nil {
		return nil, fmt.Errorf("failed to connect to GCS: %w", err)
	}
	log.G(ctx).Info("Accepted GCS connection")

	var initGuestState *gcs.InitialGuestState
	// Start the GCS protocol.
	gcc := &gcs.GuestConnectionConfig{
		Conn: conn,
		Log:  log.G(ctx).WithField(logfields.UVMID, remoteVM.ID()),
		IoListen: func(port uint32) (net.Listener, error) {
			return listenHybridVsock(udsPrefix, port)
		},
		InitGuestState: initGuestState,
	}
	return gcc.Connect(ctx, true)
}
