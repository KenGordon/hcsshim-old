//go:build !windows
// +build !windows

package main

import (
	"github.com/urfave/cli"
)

var deleteCommand = cli.Command{
	Name: "delete",
	Usage: `
This command allows containerd to delete any container resources created, mounted, and/or run by a shim when containerd can no longer communicate over rpc. This happens if a shim is SIGKILL'd with a running container. These resources will need to be cleaned up when containerd loses the connection to a shim. This is also used when containerd boots and reconnects to shims. If a bundle is still on disk but containerd cannot connect to a shim, the delete command is invoked.

The delete command will be executed in the container's bundle as its cwd.
`,
	SkipArgReorder: true,
	Action: func(context *cli.Context) (err error) {

		// research: upstream code just issues a delete command directly to runc with
		// the id of the container

		// log contents of panic log file

		// stop running pod

		return nil
	},
}
