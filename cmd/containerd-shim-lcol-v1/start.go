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
	"strings"

	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/pkg/annotations"
	"github.com/containerd/containerd/namespaces"
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
		ctx := namespaces.WithNamespace(context.Background(), namespaceFlag)
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

		/*if containerType == oci.KubernetesContainerTypeContainer {
			// TODO katiewasnothere: can we rely on reading this? I'm not sure
			shimSocketAddr, err := shim.ReadAddress(filepath.Join(cwd, "address"))
			if err != nil {
				return err
			}

			// Connect to the hosting shim and get the pid
			conn, err := shim.Connect(shimSocketAddr, shim.AnonDialer)
			if err != nil {
				return err
			}
			client := ttrpc.NewClient(conn, ttrpc.WithOnClose(func() { conn.Close() }))
			taskClient := task.NewTaskClient(client)
			connectCtx := context.Background()
			req := &task.ConnectRequest{ID: sandboxID}
			connectResp, err := taskClient.Connect(connectCtx, req)

			client.Close()
			conn.Close()
			if err != nil {
				return errors.Wrap(err, "failed to get shim pid from hosting shim")
			}
			pid = int(connectResp.ShimPid)

			// TODO katiewasnothere: go to the writing of files
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
		shimSocketAddr, err := shim.SocketAddress(ctx, addressFlag, idFlag)
		if err != nil {
			return err
		}

		socketTemp := strings.TrimPrefix(shimSocketAddr, "unix://")
		if err := os.MkdirAll(filepath.Dir(socketTemp), 777); err != nil {
			return fmt.Errorf("%s: %w", socketTemp, err)
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
			Path:   self,
			Args:   args,
			Env:    os.Environ(),
			Dir:    cwd,
			Stdin:  os.Stdin,
			Stdout: w,
			// Stderr: os.Stderr, // TODO katiewasnothere: do we want to connect this to the panic log?
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

		// Forward the invocation stderr until the serve command closes it.
		// when the serve command closes it, that indicates that the server is ready for use.
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
