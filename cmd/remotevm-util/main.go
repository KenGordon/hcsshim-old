package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/Microsoft/hcsshim/internal/vm/remotevm"
	"github.com/urfave/cli"
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
		log.Fatal(err)
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
		// Precomputed default kernel args that are used for LCOW. We can use the same.
		kernelArgs := `panic=-1 root=/dev/foo debug noapic pci=off`
		ctx := context.Background()
		builder, err := remotevm.NewUVMBuilder(
			ctx,
			vmName,
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

		if err := vm.Start(ctx); err != nil {
			return err
		}
		defer vm.Close()

		fmt.Printf("Started VM %s\n", vm.ID())

		errCh := make(chan error)
		go func() {
			fmt.Printf("Waiting on VM %s\n", vm.ID())
			if err := vm.Wait(); err != nil {
				errCh <- err
			}
		}()

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

func relayIO(r io.Reader, w io.Writer) error {
	return nil
}
