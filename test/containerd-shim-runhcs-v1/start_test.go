//go:build windows && functional
// +build windows,functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	task "github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/ttrpc"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"

	"github.com/Microsoft/hcsshim/pkg/annotations"
	"github.com/Microsoft/hcsshim/test/pkg/require"
)

func createStartCommand(t *testing.T) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	return createStartCommandWithID(t, t.Name())
}

func createStartCommandWithID(t *testing.T, id string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	bundleDir := t.TempDir()

	shim := require.BinaryInPath(t, shimExe)
	cmd := exec.Command(
		shim,
		"--namespace", t.Name(),
		"--address", "need-a-real-one",
		"--publish-binary", "need-a-real-one",
		"--id", id,
		"start",
	)
	cmd.Dir = bundleDir

	t.Logf("execing start command: %s", cmd.String())

	outb := bytes.Buffer{}
	errb := bytes.Buffer{}
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	return cmd, &outb, &errb
}

func cleanupTestBundle(t *testing.T, dir string) {
	t.Helper()
	var err error
	for i := 0; i < 2; i++ {
		// sporadic access-denies errors if trying to delete bundle (namely panic.log) before OS realizes
		// shim exited and releases dile handle
		if err = os.RemoveAll(dir); err == nil {
			// does not os.RemoveAll does not if path doesn't exist
			return
		}
		time.Sleep(time.Millisecond)
	}

	if err != nil {
		t.Errorf("failed removing test bundle with: %v", err)
	}
}

func writeBundleConfig(t *testing.T, dir string, cfg *specs.Spec) {
	t.Helper()
	cf, err := os.Create(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("failed to create config.json with error: %v", err)
	}
	err = json.NewEncoder(cf).Encode(cfg)
	if err != nil {
		cf.Close()
		t.Fatalf("failed to encode config.json with error: %v", err)
	}
	cf.Close()
}

func verifyStartCommandSuccess(t *testing.T, expectedNamespace, expectedID string, cmd *exec.Cmd, stdout, stderr *bytes.Buffer) {
	t.Helper()
	err := cmd.Run()
	if err != nil {
		t.Fatalf("expected `start` command to succeed failed with: %v, stdout: %v, stderr: %v", err, stdout.String(), stderr.String())
	}
	sout := stdout.String()
	serr := stderr.String()

	expectedStdout := fmt.Sprintf("\\\\.\\pipe\\ProtectedPrefix\\Administrators\\containerd-shim-%s-%s", expectedNamespace, expectedID)
	if !strings.HasPrefix(sout, expectedStdout) {
		t.Fatalf("expected stdout to start with: %s, got: %s, %s", expectedStdout, sout, serr)
	}
	if serr != "" {
		t.Fatalf("expected stderr to be empty got: %s", serr)
	}
	// Connect and shutdown the serve shim.
	c, err := winio.DialPipe(sout, nil)
	if err != nil {
		t.Fatalf("failed to connect to hosting shim at: %s, with: %v", sout, err)
	}
	cl := ttrpc.NewClient(c, ttrpc.WithOnClose(func() { c.Close() }))
	tc := task.NewTaskClient(cl)
	ctx := context.Background()
	req := &task.ShutdownRequest{ID: expectedID, Now: true}
	_, err = tc.Shutdown(ctx, req)

	cl.Close()
	c.Close()
	if err != nil && !strings.HasPrefix(err.Error(), "ttrpc: closed") {
		t.Fatalf("failed to shutdown shim with: %v", err)
	}
}

func Test_Start_No_Bundle_Config(t *testing.T) {
	cmd, stdout, stderr := createStartCommand(t)
	defer cleanupTestBundle(t, cmd.Dir)

	expectedStdout := ""
	expectedStderr := fmt.Sprintf(
		"open %s: The system cannot find the file specified.",
		filepath.Join(cmd.Dir, "config.json"))

	err := cmd.Run()
	verifyGlobalCommandFailure(
		t,
		expectedStdout, stdout.String(),
		expectedStderr, stderr.String(),
		err)
}

func Test_Start_Invalid_Bundle_Config(t *testing.T) {
	cmd, stdout, stderr := createStartCommand(t)
	defer cleanupTestBundle(t, cmd.Dir)

	// Write an empty file with isnt a valid json struct.
	cf, err := os.Create(filepath.Join(cmd.Dir, "config.json"))
	if err != nil {
		t.Fatalf("failed to create config.json with error: %v", err)
	}
	cf.Close()

	expectedStdout := ""
	expectedStderr := "failed to deserialize valid OCI spec"

	err = cmd.Run()
	verifyGlobalCommandFailure(
		t,
		expectedStdout, stdout.String(),
		expectedStderr, stderr.String(),
		err)
}

func Test_Start_NoPod_Config(t *testing.T) {
	cmd, stdout, stderr := createStartCommand(t)
	defer cleanupTestBundle(t, cmd.Dir)

	g, err := generate.New("windows")
	if err != nil {
		t.Fatalf("failed to generate Windows config with error: %v", err)
	}
	writeBundleConfig(t, cmd.Dir, g.Config)

	verifyStartCommandSuccess(t, t.Name(), t.Name(), cmd, stdout, stderr)
}

func Test_Start_Pod_Config(t *testing.T) {
	cmd, stdout, stderr := createStartCommand(t)
	defer cleanupTestBundle(t, cmd.Dir)

	g, err := generate.New("windows")
	if err != nil {
		t.Fatalf("failed to generate Windows config with error: %v", err)
	}
	// Setup the POD annotations
	g.AddAnnotation(annotations.KubernetesContainerType, "sandbox")
	g.AddAnnotation(annotations.KubernetesSandboxID, t.Name())

	writeBundleConfig(t, cmd.Dir, g.Config)

	verifyStartCommandSuccess(t, t.Name(), t.Name(), cmd, stdout, stderr)
}

func Test_Start_Container_InPod_Config(t *testing.T) {
	// Create the POD
	podID := t.Name() + "-POD"
	pcmd, _, _ := createStartCommandWithID(t, podID)
	defer cleanupTestBundle(t, pcmd.Dir)

	pg, perr := generate.New("windows")
	if perr != nil {
		t.Fatalf("failed to generate Windows config with error: %v", perr)
	}

	pg.AddAnnotation(annotations.KubernetesContainerType, "sandbox")
	pg.AddAnnotation(annotations.KubernetesSandboxID, podID)

	writeBundleConfig(t, pcmd.Dir, pg.Config)

	perr = pcmd.Run()
	if perr != nil {
		t.Fatalf("failed to start pod container shim with err: %v", perr)
	}

	// Create the Workload container
	wcmd, wstdout, wstderr := createStartCommand(t)
	defer cleanupTestBundle(t, wcmd.Dir)

	wg, werr := generate.New("windows")
	if werr != nil {
		t.Fatalf("failed to generate Windows config with error: %v", werr)
	}

	// Setup the POD Workload container annotations
	wg.AddAnnotation(annotations.KubernetesContainerType, "container")
	wg.AddAnnotation(annotations.KubernetesSandboxID, podID)

	writeBundleConfig(t, wcmd.Dir, wg.Config)

	verifyStartCommandSuccess(t, t.Name(), podID, wcmd, wstdout, wstderr)
}

func Test_Start_Container_InPod_Config_PodShim_Gone(t *testing.T) {
	cmd, stdout, stderr := createStartCommand(t)
	defer cleanupTestBundle(t, cmd.Dir)

	g, err := generate.New("windows")
	if err != nil {
		t.Fatalf("failed to generate Windows config with error: %v", err)
	}

	podID := "POD-TEST"
	// Setup the POD Workload container annotations
	g.AddAnnotation(annotations.KubernetesContainerType, "container")
	g.AddAnnotation("io.kubernetes.cri.sandbox-id", podID)

	writeBundleConfig(t, cmd.Dir, g.Config)

	expectedStdout := ""
	expectedStderr := "failed to connect to hosting shim"

	err = cmd.Run()
	verifyGlobalCommandFailure(
		t,
		expectedStdout, stdout.String(),
		expectedStderr, stderr.String(),
		err)
}
