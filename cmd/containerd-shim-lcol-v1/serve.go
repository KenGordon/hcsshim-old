//go:build !windows
// +build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	eventpublisher "github.com/Microsoft/hcsshim/internal/event-publisher"
	"github.com/Microsoft/hcsshim/internal/extendedtask"
	shimservice "github.com/Microsoft/hcsshim/internal/service"
	"github.com/Microsoft/hcsshim/pkg/octtrpc"
	runhcsopts "github.com/Microsoft/hcsshim/pkg/service/options"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/fifo"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sys/unix"
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
	Action: func(cliCtx *cli.Context) error {
		ctx := namespaces.WithNamespace(context.Background(), namespaceFlag)

		// get shim options
		shimOpts := &runhcsopts.Options{
			Debug:     false,
			DebugType: runhcsopts.Options_NPIPE,
		}

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

		// hook up to log file
		l := log.G(ctx)
		logrus.SetFormatter(&logrus.TextFormatter{
			TimestampFormat: log.RFC3339NanoFixed,
			FullTimestamp:   true,
		})
		f, err := openLog(ctx, idFlag)
		if err != nil {
			return err
		}
		l.Logger.SetOutput(f)
		ctx = log.WithLogger(ctx, l)

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

		socket := cliCtx.String("socket")
		socket = strings.TrimPrefix(socket, "unix://")

		// create new ttrpc server for hosting the task service
		svc, err := shimservice.NewService(shimservice.WithEventPublisher(ttrpcEventPublisher),
			shimservice.WithTID(idFlag),
			shimservice.WithIsSandbox(cliCtx.Bool("is-sandbox")),
			shimservice.WithPodFactory(&lcolPodFactory{}))
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
			if err := s.Serve(ctx, shimListener); err != nil {
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
			if !svc.GetGracefulShutdownValue() {
				// Return immediately, but still close ttrpc server, pipes, and spans
				// Shouldn't need to os.Exit without clean up (ie, deferred `.Close()`s)
				return nil
			}
			// currently the ttrpc shutdown is the only clean up to wait on
			sctx, cancel := context.WithTimeout(ctx, gracefulShutdownTimeout)
			defer cancel()
			err = s.Shutdown(sctx)
		}

		return nil
	},
}

func openLog(ctx context.Context, _ string) (io.Writer, error) {
	return fifo.OpenFifoDup2(ctx, "log", unix.O_WRONLY, 0700, int(os.Stderr.Fd()))
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
