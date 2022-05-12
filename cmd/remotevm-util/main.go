package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/logfields"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vm/remotevm"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

const usage = `remotevm-util is a simple tool to test out remotevm implementations`

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
)

func listenVsock(ctx context.Context, port uint32, uvm vm.UVM) (net.Listener, error) {
	vmsocket, ok := uvm.(vm.VMSocketManager)
	if !ok {
		return nil, fmt.Errorf("stopping vm socket configuration: %w", vm.ErrNotSupported)
	}
	return vmsocket.VMSocketListen(ctx, vm.VSock, port)
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
	},
	Action: func(clictx *cli.Context) error {
		kernelArgs := `8250_core.nr_uarts=0 panic=-1 quiet pci=off brd.rd_nr=0 pmtmr=0 -- -e 1 /bin/vsockexec -e 109 /bin/gcs -v4 -log-format json -disable-time-sync -loglevel debug`
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

		opts := []vm.CreateOpt{remotevm.WithIgnoreSupported()}
		vm, err := builder.Create(ctx, opts)
		if err != nil {
			return err
		}

		gc, err := vmStart(ctx, vm)
		if err != nil {
			return err
		}

		errCh := make(chan error)
		go func() {
			fmt.Printf("Waiting on VM %s\n", vm.ID())
			if err := vm.Wait(); err != nil {
				errCh <- err
			}
		}()
		fmt.Printf("Protocol in use: %d\n", gc.Protocol())

		// lpp := &lcowProcessParameters{
		// 	ProcessParameters: hcsschema.ProcessParameters{
		// 		CreateStdInPipe:  c.Stdin != nil,
		// 		CreateStdOutPipe: c.Stdout != nil,
		// 		CreateStdErrPipe: c.Stderr != nil,
		// 	},
		// 	OCIProcess: c.Spec,
		// }
		// p, err := gc.CreateProcess(ctx, )

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		select {
		case <-sigChan:
			fmt.Println("Received signal, exiting.")
			if err := vm.Stop(ctx); err != nil {
				fmt.Fprintf(os.Stderr, err.Error())
				os.Exit(1)
			}
			os.Exit(0)
		case err := <-errCh:
			if err != nil {
				return err
			}
		}

		return vm.ExitError()
	},
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
		fmt.Println("Accepted connection")
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
func vmStart(ctx context.Context, vm vm.UVM) (*gcs.GuestConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	g, gctx := errgroup.WithContext(ctx)
	defer func() {
		_ = g.Wait()
	}()
	defer cancel()

	fmt.Println("Creating entropy listener")
	entropyListener, err := listenVsock(context.Background(), 1, vm)
	if err != nil {
		return nil, err
	}

	fmt.Println("Creating output listener")
	outputListener, err := listenVsock(context.Background(), 109, vm)
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
		go func() {
			outputHandler(conn)
		}()
		return nil
	})

	if err := vm.Start(context.Background()); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			vm.Close()
		}
	}()

	// Collect any errors from writing entropy or establishing the log
	// connection.
	fmt.Println("Waiting on vsock conns")
	if err = g.Wait(); err != nil {
		return nil, err
	}

	fmt.Printf("Creating GCS listener\n")
	gcListener, err := listenVsock(context.Background(), gcs.LinuxGcsVsockPort, vm)
	if err != nil {
		return nil, err
	}

	// Accept the GCS connection.
	conn, err := acceptAndClose(ctx, gcListener)
	gcListener = nil
	if err != nil {
		return nil, fmt.Errorf("failed to connect to GCS: %w", err)
	}

	var initGuestState *gcs.InitialGuestState
	// Start the GCS protocol.
	gcc := &gcs.GuestConnectionConfig{
		Conn: conn,
		Log:  log.G(ctx).WithField(logfields.UVMID, vm.ID()),
		IoListen: func(port uint32) (net.Listener, error) {
			return listenVsock(context.Background(), port, vm)
		},
		InitGuestState: initGuestState,
	}
	return gcc.Connect(ctx, true)
}

func outputHandler(r io.Reader) {
	_, _ = io.Copy(os.Stdout, r)
}
