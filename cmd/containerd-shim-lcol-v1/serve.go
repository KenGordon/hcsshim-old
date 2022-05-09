//go:build !windows
// +build !windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	runhcsopts "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/urfave/cli"
)

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

		// hook up to panic.log

		// create an event publisher for ttrpc address to containerd

		// create new ttrpc server for hosting the task service

		// register the services that the ttrpc server will host

		// configure ttrpc server to listen on socket that was created during start command

		socket := ctx.String("socket")
		if !strings.HasSuffix(socket, `.sock`) {
			return errors.New("socket is required to be a linux socket address")
		}

		// wait for the shim to exit

		// wait for ttrpc server to be shutdown

		return nil
	},
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
