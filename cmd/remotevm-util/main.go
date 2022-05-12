package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/Microsoft/hcsshim/internal/guestpath"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/logfields"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vm/remotevm"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

const usage = `remotevm-util is a simple tool to test out remotevm implementations. It will launch a vm
using the supplied kernel and rootfs and then launch a shell inside.`

func main() {
	app := cli.NewApp()
	app.Name = "remotevm-util"
	app.Commands = []cli.Command{
		launchVMCommand,
	}
	app.Usage = usage

	if err := app.Run(os.Args); err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(1)
	}
}

const (
	kernelPath = "kernel"
	initrdPath = "initrd"
	binPath    = "binpath"
	ttrpcAddr  = "ttrpc"
	vmName     = "name"
	blockDev   = "blockdev"
)

func listenVsock(ctx context.Context, port uint32, uvm vm.UVM) (net.Listener, error) {
	vmsocket, ok := uvm.(vm.VMSocketManager)
	if !ok {
		return nil, fmt.Errorf("stopping vm socket configuration: %w", vm.ErrNotSupported)
	}
	return vmsocket.VMSocketListen(ctx, vm.VSock, port)
}

// Additional fields to hcsschema.ProcessParameters used by LCOW
type lcowProcessParameters struct {
	hcsschema.ProcessParameters
	OCIProcess *specs.Process `json:"OciProcess,omitempty"`
}

// Get a random unix socket address to use. The "randomness" equates to makes a temp file to reserve a name
// and then shortly after deleting it and using this as the socket address.
func randomUnixSockAddr() (string, error) {
	// Make a temp file and delete to "reserve" a unique name for the unix socket
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp file for unix socket")
	}

	if err := f.Close(); err != nil {
		return "", errors.Wrap(err, "failed to close temp file")
	}

	if err := os.Remove(f.Name()); err != nil {
		return "", errors.Wrap(err, "failed to delete temp file to free up name")
	}

	return f.Name(), nil
}

var launchVMCommand = cli.Command{
	Name:  "launch",
	Usage: "Launches a VM using the specified kernel and initrd",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:     kernelPath,
			Usage:    "Specifies path to the kernel to boot off of",
			Required: true,
		},
		cli.StringFlag{
			Name:     initrdPath,
			Usage:    "Specifies path of the initrd to use as the rootfs",
			Required: true,
		},
		cli.StringFlag{
			Name:     binPath,
			Usage:    "Path to the binary implementing the vmservice interface",
			Required: true,
		},
		cli.StringFlag{
			Name:     vmName,
			Usage:    "Specifies the name to use for the VM",
			Required: true,
		},
		cli.StringSliceFlag{
			Name:  blockDev,
			Usage: "Specifies path(s) to files representing block devices to add to the VM",
		},
	},
	Action: func(clictx *cli.Context) error {
		kernelArgs := `8250_core.nr_uarts=1 8250_core.skip_txen_test=1 console=ttyS0,115200 pci=off brd.rd_nr=0 pmtmr=0 -- -e 1 /bin/vsockexec -e 109 /bin/gcs -v4 -log-format json -disable-time-sync -loglevel debug`
		ctx := context.Background()
		builder, err := remotevm.NewUVMBuilder(
			ctx,
			clictx.String(vmName),
			os.Args[0],
			clictx.String(binPath),
			clictx.String(ttrpcAddr),
			vm.Linux,
		)
		if err != nil {
			return err
		}

		boot := builder.(vm.BootManager)
		if err := boot.SetLinuxKernelDirectBoot(
			clictx.String(kernelPath),
			clictx.String(initrdPath),
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

		opts := []vm.CreateOpt{remotevm.WithIgnoreSupported()}
		remoteVM, err := builder.Create(ctx, opts)
		if err != nil {
			return err
		}

		gc, err := vmStart(ctx, remoteVM)
		if err != nil {
			return err
		}

		errCh := make(chan error)
		go func() {
			fmt.Printf("Waiting on VM %s\n", remoteVM.ID())
			if err := remoteVM.Wait(); err != nil {
				errCh <- err
			}
		}()
		fmt.Printf("Protocol in use: %d\n", gc.Protocol())

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

		stdin, stdout, stderr := p.Stdio()

		logEntry := log.G(ctx)
		go func() {
			n, err := relayIO(os.Stdout, stdout, logEntry, "stdout")
			if err != nil {
				logEntry.WithError(err).Warn("piping stdout failed")
			}
			logEntry.Infof("finished piping %d bytes from stdout", n)
		}()

		go func() {
			n, err := relayIO(os.Stdout, stderr, logEntry, "stderr")
			if err != nil {
				logEntry.WithError(err).Warn("piping stderr failed")
			}
			logEntry.Infof("finished piping %d bytes from stderr", n)
		}()

		go func() {
			n, err := relayIO(stdin, os.Stdin, logEntry, "stdin")
			if err != nil {
				logEntry.WithError(err).Warn("piping stdin failed")
			}
			logEntry.Infof("finished piping %d bytes from stdin", n)
		}()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		select {
		case <-sigChan:
			log.G(ctx).Info("Received signal, exiting.")
			if err := remoteVM.Stop(ctx); err != nil {
				return err
			}
			return nil
		case err := <-errCh:
			if err != nil {
				return err
			}
		}

		return remoteVM.ExitError()
	},
}

// relayIO is a glorified io.Copy that also logs when the copy has completed.
func relayIO(w io.Writer, r io.Reader, log *logrus.Entry, name string) (int64, error) {
	n, err := io.Copy(w, r)
	if log != nil {
		lvl := logrus.DebugLevel
		log = log.WithFields(logrus.Fields{
			"file":  name,
			"bytes": n,
		})
		if err != nil {
			lvl = logrus.ErrorLevel
			log = log.WithError(err)
		}
		log.Log(lvl, "Cmd IO relay complete")
	}
	return n, err
}

// acceptAndClose accepts a connection and then closes a listener. If the
// context becomes done or the utility VM terminates, the operation will be
// cancelled (but the listener will still be closed).
func acceptAndClose(ctx context.Context, l net.Listener) (net.Conn, error) {
	var conn net.Conn
	ch := make(chan error)
	go func() {
		var err error
		conn, err = l.Accept()
		ch <- err
	}()
	select {
	case err := <-ch:
		l.Close()
		return conn, err
	case <-ctx.Done():
	}
	l.Close()
	err := <-ch
	if err == nil {
		return conn, err
	}
	// Prefer context error to VM error to accept error in order to return the
	// most useful error.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}

// vmStart sets up necessary vsock connections and starts the VM.
func vmStart(ctx context.Context, remoteVM vm.UVM) (*gcs.GuestConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	g, gctx := errgroup.WithContext(ctx)
	defer func() {
		_ = g.Wait()
	}()
	defer cancel()

	fmt.Println("Creating entropy listener")
	entropyListener, err := listenVsock(context.Background(), 1, remoteVM)
	if err != nil {
		return nil, err
	}

	fmt.Println("Creating output listener")
	outputListener, err := listenVsock(context.Background(), 109, remoteVM)
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
		fmt.Println("Accepted entropy socket connection")
		_, err = io.CopyN(conn, rand.Reader, 512)
		if err != nil {
			return fmt.Errorf("failed to write entropy: %s", err)
		}
		fmt.Println("Entropy socket finished piping bytes")
		return nil
	})

	g.Go(func() error {
		conn, err := acceptAndClose(gctx, outputListener)
		outputListener = nil
		if err != nil {
			return fmt.Errorf("failed to connect to log socket: %s", err)
		}
		fmt.Println("Accepted log output connection")
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
	fmt.Println("Waiting on vsock conns")
	if err = g.Wait(); err != nil {
		return nil, err
	}

	fmt.Printf("Creating GCS listener\n")
	gcListener, err := listenVsock(context.Background(), gcs.LinuxGcsVsockPort, remoteVM)
	if err != nil {
		return nil, err
	}

	// Accept the GCS connection.
	conn, err := acceptAndClose(ctx, gcListener)
	gcListener = nil
	if err != nil {
		return nil, fmt.Errorf("failed to connect to GCS: %w", err)
	}
	fmt.Println("Accepted GCS connection")

	var initGuestState *gcs.InitialGuestState
	// Start the GCS protocol.
	gcc := &gcs.GuestConnectionConfig{
		Conn: conn,
		Log:  log.G(ctx).WithField(logfields.UVMID, remoteVM.ID()),
		IoListen: func(port uint32) (net.Listener, error) {
			return listenVsock(context.Background(), port, remoteVM)
		},
		InitGuestState: initGuestState,
	}
	return gcc.Connect(ctx, true)
}

func outputHandler(r io.Reader) {
	_, _ = io.Copy(os.Stdout, r)
}
