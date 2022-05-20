//go:build !windows
// +build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/pkg/annotations"
	"github.com/containerd/containerd/runtime/v2/shim"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var startCommand = cli.Command{
	Name: "start",
	Usage: `
This command will launch new shims.

The start command, as well as all binary calls to the shim, has the bundle for the container set as the cwd.

The start command MUST return an address to a shim for containerd to issue API requests for container operations.

The start command can either start a new shim or return an address to an existing shim based on the shim's logic.
`,
	SkipArgReorder: true,
	Action: func(cliCtx *cli.Context) (err error) {
		// We cant write anything to stdout/stderr for this cmd.
		ctx := context.Background()
		logrus.SetOutput(ioutil.Discard)

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		a, err := getSpecAnnotations(cwd)
		if err != nil {
			return err
		}

		// need to refactor stuff
		containerType, sandboxID, err := oci.GetSandboxTypeAndID(a)
		if err != nil {
			return err
		}

		// TODO katiewasnothere: what are the scenarios in which this code block is executed?
		/*if ct == oci.KubernetesContainerTypeContainer {
			// TODO katiewasnothere: use better variable names so this address is not confused with
			// addressFlag
			address = fmt.Sprintf(addrFmt, namespaceFlag, sbid)

			// Connect to the hosting shim and get the pid
			c, err := winio.DialPipe(address, nil)
			if err != nil {
				return errors.Wrap(err, "failed to connect to hosting shim")
			}
			cl := ttrpc.NewClient(c, ttrpc.WithOnClose(func() { c.Close() }))
			t := task.NewTaskClient(cl)
			ctx := gocontext.Background()
			req := &task.ConnectRequest{ID: sbid}
			cr, err := t.Connect(ctx, req)

			cl.Close()
			c.Close()
			if err != nil {
				return errors.Wrap(err, "failed to get shim pid from hosting shim")
			}
			pid = int(cr.ShimPid)
		}*/

		// We need to serve a new one.
		isSandbox := containerType == oci.KubernetesContainerTypeSandbox
		if isSandbox && idFlag != sandboxID {
			return errors.Errorf(
				"'id' and '%s' must match for '%s=%s'",
				annotations.KubernetesSandboxID,
				annotations.KubernetesContainerType,
				oci.KubernetesContainerTypeSandbox)
		}

		// construct socket name
		// TODO katiewasnothere: not sure if idFlag or sandboxID should be used here?
		shimSocketAddr, err := shim.SocketAddress(ctx, addressFlag, idFlag)
		if err != nil {
			return err
		}

		// create a new socket
		shimSocket, err := shim.NewSocket(shimSocketAddr)
		if err != nil {
			return err
		}

		// get socket file so serve command can inheriet
		socketFile, err := shimSocket.File()
		if err != nil {
			return err
		}

		self, err := os.Executable()
		if err != nil {
			return err
		}

		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		defer r.Close()
		defer w.Close()

		panicLogFile, err := os.Create(filepath.Join(cwd, "panic.log"))
		if err != nil {
			return err
		}
		defer panicLogFile.Close()

		args := []string{
			self,
			"--namespace", namespaceFlag,
			"--address", addressFlag,
			"--publish-binary", containerdBinaryFlag,
			"--id", idFlag,
			"serve",
			"--socket", shimSocketAddr,
		}
		if isSandbox {
			args = append(args, "--is-sandbox")
		}
		cmd := &exec.Cmd{
			Path:       self,
			Args:       args,
			Env:        os.Environ(),
			Dir:        cwd,
			Stdin:      os.Stdin,
			Stdout:     w,
			Stderr:     panicLogFile,
			ExtraFiles: []*os.File{socketFile},
		}

		if err := cmd.Start(); err != nil {
			return err
		}
		w.Close()
		defer func() {
			if err != nil {
				_ = cmd.Process.Kill()
			}
		}()

		// TODO katiewasnothere: what does this do? Forward the invocation stderr until the serve command closes it.
		_, err = io.Copy(os.Stderr, r)
		if err != nil {
			return err
		}

		pid := cmd.Process.Pid

		if err := shim.WritePidFile(filepath.Join(cwd, "shim.pid"), pid); err != nil {
			return err
		}

		if err := shim.WriteAddress(filepath.Join(cwd, "address"), shimSocketAddr); err != nil {
			return err
		}

		// Write the address new or existing to stdout
		if _, err := fmt.Fprint(os.Stdout, shimSocketAddr); err != nil {
			return err
		}
		return nil
	},
}

func getSpecAnnotations(bundlePath string) (map[string]string, error) {
	// specAnnotations is a minimal representation for oci.Spec that we need
	// to serve a shim.
	type specAnnotations struct {
		// Annotations contains arbitrary metadata for the container.
		Annotations map[string]string `json:"annotations,omitempty"`
	}
	f, err := os.OpenFile(filepath.Join(bundlePath, "config.json"), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var spec specAnnotations
	if err := json.NewDecoder(f).Decode(&spec); err != nil {
		return nil, fmt.Errorf("failed to deserialize valid OCI spec: %w", err)
	}
	return spec.Annotations, nil
}
