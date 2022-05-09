//go:build !windows
// +build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/hcsshim/internal/oc"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
	"go.opencensus.io/trace"
)

const usage = ``
const ttrpcAddressEnv = "TTRPC_ADDRESS"

// version will be populated by the Makefile, read from
// VERSION file of the source code.
var version = ""

// gitCommit will be the hash that the binary was built from
// and will be populated by the Makefile
var gitCommit = ""

var (
	namespaceFlag        string
	addressFlag          string
	containerdBinaryFlag string

	idFlag string

	// gracefulShutdownTimeout is how long to wait for clean-up before just exiting
	gracefulShutdownTimeout = 3 * time.Second
)

func main() {

	// Register our OpenCensus logrus exporter
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	trace.RegisterExporter(&oc.LogrusExporter{})

	app := cli.NewApp()
	app.Name = "containerd-shim-lcol-v1"
	app.Usage = usage

	var v []string
	if version != "" {
		v = append(v, version)
	}
	if gitCommit != "" {
		v = append(v, fmt.Sprintf("commit: %s", gitCommit))
	}
	v = append(v, fmt.Sprintf("spec: %s", specs.Version))
	app.Version = strings.Join(v, "\n")

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "namespace",
			Usage: "the namespace of the container",
		},
		cli.StringFlag{
			Name:  "address",
			Usage: "the address of the containerd's main socket",
		},
		cli.StringFlag{
			Name:  "publish-binary",
			Usage: "the binary path to publish events back to containerd",
		},
		cli.StringFlag{
			Name:  "id",
			Usage: "the id of the container",
		},
		cli.StringFlag{
			Name:  "bundle",
			Usage: "the bundle path to delete (delete command only).",
		},
		cli.BoolFlag{
			Name:  "debug",
			Usage: "run the shim in debug mode",
		},
	}
	app.Commands = []cli.Command{
		startCommand,
		deleteCommand,
		serveCommand,
	}
	app.Before = func(context *cli.Context) error {
		if namespaceFlag = context.GlobalString("namespace"); namespaceFlag == "" {
			return errors.New("namespace is required")
		}
		if addressFlag = context.GlobalString("address"); addressFlag == "" {
			return errors.New("address is required")
		}
		if containerdBinaryFlag = context.GlobalString("publish-binary"); containerdBinaryFlag == "" {
			return errors.New("publish-binary is required")
		}
		if idFlag = context.GlobalString("id"); idFlag == "" {
			return errors.New("id is required")
		}
		return nil
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(cli.ErrWriter, err)
		os.Exit(1)
	}
}
