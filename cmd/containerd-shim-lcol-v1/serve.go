//go:build !windows
// +build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	runhcsopts "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	eventpublisher "github.com/Microsoft/hcsshim/internal/event-publisher"
	"github.com/Microsoft/hcsshim/internal/extendedtask"
	shimservice "github.com/Microsoft/hcsshim/internal/shim-service"
	"github.com/Microsoft/hcsshim/pkg/octtrpc"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

// copied from upstream
const socketPathLimit = 106

var serveCommand = cli.Command{
	Name:           "serve",
	Hidden:         true,
	SkipArgReorder: true,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "socket",
			Usage: "the socket path to serve",
		},
		cli.BoolFlag{
			Name:  "is-sandbox",
			Usage: "is the task id a Kubernetes sandbox id",
		},
	},
	Action: func(ctx *cli.Context) error {

		// get shim options
		shimOpts := &runhcsopts.Options{
			Debug:     false,
			DebugType: runhcsopts.Options_NPIPE,
		}

		// containerd passes the shim options protobuf via stdin.
		newShimOpts, err := readOptions(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read shim options from stdin: %w", err)
		} else if newShimOpts != nil {
			// We received a valid shim options struct.
			shimOpts = newShimOpts
		}

		// setup logging for shim

		// If log level is specified, set the corresponding logrus logging level. This overrides the debug option
		// (unless the level being asked for IS debug also, then this doesn't do much).
		if shimOpts.LogLevel != "" {
			lvl, err := logrus.ParseLevel(shimOpts.LogLevel)
			if err != nil {
				return fmt.Errorf("failed to parse shim log level %q: %w", shimOpts.LogLevel, err)
			}
			logrus.SetLevel(lvl)
		}

		// close stdin since we've read everything we need from it
		os.Stdin.Close()

		// hook up to panic.log

		// create an event publisher for ttrpc address to containerd
		ttrpcAddress := os.Getenv(ttrpcAddressEnv)
		ttrpcEventPublisher, err := eventpublisher.NewEventPublisher(ttrpcAddress, namespaceFlag)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				ttrpcEventPublisher.Close()
			}
		}()

		socket := ctx.String("socket")
		if !strings.HasSuffix(socket, `.sock`) {
			return errors.New("socket is required to be a linux socket address")
		}

		// create new ttrpc server for hosting the task service
		svc, err := NewService(shimservice.WithEventPublisher(ttrpcEventPublisher),
			shimservice.WithTID(idFlag),
			shimservice.WithIsSandbox(ctx.Bool("is-sandbox")))
		if err != nil {
			return fmt.Errorf("failed to create new service: %w", err)
		}

		s, err := ttrpc.NewServer(ttrpc.WithUnaryServerInterceptor(octtrpc.ServerInterceptor()))
		if err != nil {
			return err
		}
		defer s.Close()

		// register the services that the ttrpc server will host
		task.RegisterTaskService(s, svc)
		extendedtask.RegisterExtendedTaskService(s, svc)

		// listen on socket
		shimListener, err := serveListener(socket)
		if err != nil {
			return err
		}
		defer shimListener.Close()

		serrs := make(chan error, 1)
		defer close(serrs)
		go func() {
			// Serve loops infinitely unless s.Shutdown or s.Close are called.
			// Passed in context is used as parent context for handling requests,
			// but canceliing does not bring down ttrpc service.

			// configure ttrpc server to listen on socket that was created during start command
			if err := s.Serve(context.Background(), shimListener); err != nil {
				logrus.WithError(err).Fatal("containerd-shim: ttrpc server failure")
				serrs <- err
				return
			}
			serrs <- nil
		}()

		// wait briefly to see if we immediately run into any errors serving the task service
		select {
		case err := <-serrs:
			return err
		case <-time.After(2 * time.Millisecond):
			// This is our best indication that we have not errored on creation
			// and are successfully serving the API.
			// Closing stdout signals to containerd that shim started successfully
			os.Stdout.Close()
		}

		// wait for ttrpc server to be shutdown
		select {
		case err = <-serrs:
			// the ttrpc server shutdown without processing a shutdown request
		case <-svc.Done():
			if !svc.gracefulShutdown {
				// Return immediately, but still close ttrpc server, pipes, and spans
				// Shouldn't need to os.Exit without clean up (ie, deferred `.Close()`s)
				return nil
			}
			// currently the ttrpc shutdown is the only clean up to wait on
			sctx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			defer cancel()
			err = s.Shutdown(sctx)
		}

		return nil
	},
}

// taken from https://github.com/containerd/containerd/blob/6fda809e1b81928722de452a756df33aa9a5c998/runtime/v2/shim/shim_unix.go
func serveListener(path string) (net.Listener, error) {
	var (
		l   net.Listener
		err error
	)
	if path == "" {
		l, err = net.FileListener(os.NewFile(3, "socket"))
		path = "[inherited from parent]"
	} else {
		if len(path) > socketPathLimit {
			return nil, fmt.Errorf("%q: unix socket path too long (> %d)", path, socketPathLimit)
		}
		l, err = net.Listen("unix", path)
	}
	if err != nil {
		return nil, err
	}
	logrus.WithField("socket", path).Debug("serving api on socket")
	return l, nil
}

// readOptions reads in bytes from the reader and converts it to a shim options
// struct. If no data is available from the reader, returns (nil, nil).
func readOptions(r io.Reader) (*runhcsopts.Options, error) {
	d, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}
	if len(d) > 0 {
		var a types.Any
		if err := proto.Unmarshal(d, &a); err != nil {
			return nil, fmt.Errorf("failed unmarshalling into Any: %w", err)
		}
		v, err := typeurl.UnmarshalAny(&a)
		if err != nil {
			return nil, fmt.Errorf("failed unmarshalling by typeurl: %w", err)
		}
		return v.(*runhcsopts.Options), nil
	}
	return nil, nil
}
