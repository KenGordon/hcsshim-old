package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/hcs"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/signals"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/Microsoft/hcsshim/osversion"
	"github.com/Microsoft/hcsshim/pkg/annotations"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/runtime/task/v2"
	containerd_v1_types "github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/runtime"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newHcsVmExec(
	ctx context.Context,
	events publisher,
	tid string,
	c cow.Container,
	id string,
	bundle string,
	spec *specs.Spec,
	host *uvm.UtilityVM,
	_ string,
	io cmd.UpstreamIO,
	r *resources.Resources,
) shimExec {
	log.G(ctx).WithFields(logrus.Fields{
		"tid":    tid,
		"eid":    id,
		"bundle": bundle,
	}).Debug("newHcsVmExec")

	hve := &hcsVMExec{
		events:                 events,
		tid:                    tid,
		host:                   host,
		c:                      c,
		id:                     id,
		bundle:                 bundle,
		s:                      spec,
		processDone:            make(chan struct{}),
		state:                  shimExecStateCreated,
		exitStatus:             255,
		exited:                 make(chan struct{}),
		io:                     io,
		resources:              r,
		terminateOnProcessExit: !oci.ParseAnnotationsBool(ctx, spec.Annotations, annotations.NoTerminateOnAppExit, false),
	}
	go hve.waitForContainerExit()
	return hve
}

type hcsVMExec struct {
	events publisher
	tid    string

	// TODO: the two are basically the same, maybe we can just keep track of one.
	host *uvm.UtilityVM
	c    cow.Container

	id     string
	bundle string

	// TODO: spec needed only to add mounts and configure networking.
	s               *specs.Spec
	io              cmd.UpstreamIO
	processDone     chan struct{}
	processDoneOnce sync.Once

	//resources *resources.Resources
	//owner string

	sl                     sync.Mutex
	state                  shimExecState
	pid                    int
	exitStatus             uint32
	exitedAt               time.Time
	p                      *cmd.Cmd
	terminateOnProcessExit bool

	resources *resources.Resources

	exited     chan struct{}
	exitedOnce sync.Once
}

func (hve *hcsVMExec) ID() string {
	return hve.id
}

func (hve *hcsVMExec) Pid() int {
	return 0
}

func (hve *hcsVMExec) State() shimExecState {
	hve.sl.Lock()
	defer hve.sl.Unlock()
	return hve.state
}

func (hve *hcsVMExec) Status() *task.StateResponse {
	hve.sl.Lock()
	defer hve.sl.Unlock()

	var s containerd_v1_types.Status
	switch hve.state {
	case shimExecStateCreated:
		s = containerd_v1_types.Status_CREATED
	case shimExecStateRunning:
		s = containerd_v1_types.Status_RUNNING
	case shimExecStateExited:
		s = containerd_v1_types.Status_STOPPED
	}

	return &task.StateResponse{
		ID:       hve.tid,
		ExecID:   hve.id,
		Bundle:   hve.bundle,
		Status:   s,
		ExitedAt: timestamppb.New(hve.exitedAt),
	}
}

type netSh struct {
	ctx  context.Context
	host *uvm.UtilityVM
}

func (ns *netSh) makeArgs(extra []string) []string {
	return append([]string{"netsh", "int", "ipv4"}, extra...)
}

func (ns *netSh) AssignIP(adapter, addr, gate string) error {
	addIP := ns.makeArgs([]string{
		"set",
		"address",
		fmt.Sprintf("name=%s", adapter),
		"static",
		addr,
		"mask=255.255.0.0",
		fmt.Sprintf("gateway=%s", gate),
	})
	stdout, stderr, err := ns.exec(addIP)
	if err != nil {
		return fmt.Errorf("failed to assign IP: stdout: %s, stderr: %s: %w", stdout, stderr, err)
	}
	return nil
}

func (ns *netSh) GetAdapterIndex(name string) (string, error) {
	showInterfaces := ns.makeArgs([]string{"sho", "int"})
	stdout, _, err := ns.exec(showInterfaces)
	if err != nil {
		return "", err
	}
	stdout = strings.TrimSpace(stdout)
	lines := strings.Split(stdout, "\n")
	for _, l := range lines {
		if !strings.Contains(l, name) {
			continue
		}
		l = strings.TrimSpace(l)
		return strings.Split(l, " ")[0], nil
	}
	return "", fmt.Errorf("no interface with name %s found", name)
}

func (ns *netSh) AddDNS(interfaceName string, serverList []string) error {
	if len(serverList) < 1 {
		return fmt.Errorf("expected at least one DNS server, got %d", len(serverList))
	}

	for index, server := range serverList {
		dnsAdd := ns.makeArgs([]string{
			"add",
			"dns",
			fmt.Sprintf("name=%s", interfaceName),
			server,
			fmt.Sprintf("index=%d", index),
		})
		stdout, stderr, err := ns.exec(dnsAdd)
		if err != nil {
			return fmt.Errorf("failed to add DNS: stdout: %s, stderr: %s: %w", stdout, stderr, err)
		}
	}
	return nil
}

func (ns *netSh) exec(args []string) (string, string, error) {
	errBuf := &bytes.Buffer{}
	stderr, err := cmd.CreatePipeAndListen(errBuf, false)
	if err != nil {
		return "", "", err
	}
	outBuf := &bytes.Buffer{}
	stdout, err := cmd.CreatePipeAndListen(outBuf, false)
	if err != nil {
		return "", "", err
	}

	log.G(ns.ctx).WithField("netsh", strings.Join(args, " ")).Debug("invoking netsh")
	cmdReq := &cmd.CmdProcessRequest{
		Args:   args,
		Stdout: stdout,
		Stderr: stderr,
	}
	if _, err := cmd.ExecInUvm(ns.ctx, ns.host, cmdReq); err != nil {
		return outBuf.String(), errBuf.String(), err
	}
	return outBuf.String(), outBuf.String(), nil
}

func (hve *hcsVMExec) startInternal(ctx context.Context, initializeContainer bool) (err error) {
	hve.sl.Lock()
	defer hve.sl.Unlock()
	if hve.state != shimExecStateCreated {
		return newExecInvalidStateError(hve.tid, hve.id, hve.state, "start")
	}
	defer func() {
		if err != nil {
			log.G(ctx).Debug("exitFromCreatedL")
			hve.exitFromCreatedL(ctx, 1)
		}
	}()
	if initializeContainer {
		log.G(ctx).Debug("starting hcsVMExec")
		err = hve.c.Start(ctx)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				log.G(ctx).Debug("terminating")
				_ = hve.c.Terminate(ctx)
				hve.c.Close()
			}
		}()
	}

	nsid := ""
	if hve.s.Windows != nil && hve.s.Windows.Network != nil {
		nsid = hve.s.Windows.Network.NetworkNamespace
	}

	log.G(ctx).WithField("nsid", nsid).Debug("setting up networking")
	if nsid != "" {
		if err := hve.host.ConfigureNetworking(ctx, nsid); err != nil {
			return fmt.Errorf("failed to setup networking for VM: %w", err)
		}
	}

	// Manually assign IP address and DNS
	// FIXME: Ideally we should figure out how to assign the IP address to the
	//   host compartment.
	endpoints, err := uvm.GetNamespaceEndpoints(ctx, nsid)
	if err != nil {
		return err
	}
	if len(endpoints) != 1 {
		return fmt.Errorf("expected exactly 1 endpoint got %d", len(endpoints))
	}

	// FIXME: sleep for a few seconds for adapter to appear
	time.Sleep(2 * time.Second)
	ns := &netSh{
		host: hve.host,
		ctx:  ctx,
	}

	// FIXME: Seems like the adapter always has index "5", but we need to wait
	//   a bit until it becomes available.
	adapterIndex, err := ns.GetAdapterIndex("Ethernet")
	if err != nil {
		return err
	}

	if err := ns.AssignIP(adapterIndex, endpoints[0].IPAddress.String(), endpoints[0].GatewayAddress); err != nil {
		return err
	}

	if err := ns.AddDNS(adapterIndex, strings.Split(endpoints[0].DNSServerList, ",")); err != nil {
		return err
	}

	if !oci.ParseAnnotationsBool(ctx, hve.s.Annotations, annotations.NoVSMBSetup, false) {
		errBuff := &bytes.Buffer{}
		stderr, err := cmd.CreatePipeAndListen(errBuff, false)
		if err != nil {
			return err
		}
		outBuff := &bytes.Buffer{}
		stdout, err := cmd.CreatePipeAndListen(outBuff, false)
		if err != nil {
			return err
		}
		vsmbStart := &cmd.CmdProcessRequest{
			Args:   []string{`C:\vsmbcontrol.exe`, "-start"},
			Stdout: stdout,
			Stderr: stderr,
		}
		if _, err := cmd.ExecInUvm(ctx, hve.host, vsmbStart); err != nil {
			log.G(ctx).WithField("stdout", outBuff.String()).Debug("vsmbcontrol.exe stdout")
			log.G(ctx).WithField("stderr", errBuff.String()).Debug("vsmbcontrol.exe stderr")
			return err
		}
		time.Sleep(time.Second)

		var uvmPaths []string
		// setting up VSMB shares
		for _, m := range hve.s.Mounts {
			if uvm.IsPipe(m.Source) {
				// add named pipe
				log.G(ctx).WithFields(logrus.Fields{
					"host_path": m.Source,
				}).Debug("adding pipe")
				pm, err := hve.host.AddPipeMount(ctx, m.Source, m.Destination)
				if err != nil {
					return err
				}
				hve.resources.Add(pm)
				continue
			}
			// virtual/physical disks are not supported for now
			if m.Type != "" {
				return errors.New("the only supported mount type is a share type")
			}

			readonly := false
			for _, opt := range m.Options {
				if opt == "ro" {
					readonly = true
					break
				}
			}
			if err := hve.host.Share(ctx, m.Source, m.Destination, readonly); err != nil {
				return fmt.Errorf("failed to share host path: %w", err)
			}
			uvmPaths = append(uvmPaths, m.Destination)
		}

		go func() {
			handleExec := &cmd.CmdProcessRequest{
				Args: append([]string{`C:\handle.exe`}, uvmPaths...),
			}
			if _, err := cmd.ExecInUvm(ctx, hve.host, handleExec); err != nil {
				log.G(ctx).WithError(err).Error("failed to exec in UVM")
			}
		}()
	}

	log.G(ctx).WithFields(logrus.Fields{
		"CommandLine": hve.s.Process.CommandLine,
		"Args":        hve.s.Process.Args,
		"env":         hve.s.Process.Env,
		"stdout":      hve.io.Stdout(),
		"stderr":      hve.io.Stderr(),
	}).Debug("hve.s.Process command line and args")
	// Launch app based on command line and args/env from process.

	hve.state = shimExecStateRunning
	// TODO: we may need to update AzCRI to not populate the process spec and
	// also accept empty images. Maybe we could repurpose the field and instead
	// of image reference it'll be a path to VHDX image?
	if hve.s.Process != nil {
		p := &cmd.Cmd{
			Host:   hve.c,
			Stdin:  hve.io.Stdin(),
			Stdout: hve.io.Stdout(),
			Stderr: hve.io.Stderr(),
			Spec:   hve.s.Process,
			Log: log.G(ctx).WithFields(logrus.Fields{
				"tid": hve.tid,
				"eid": hve.id,
			}),
			CopyAfterExitTimeout: time.Second,
		}
		if err := p.Start(); err != nil {
			if hve.terminateOnProcessExit {
				hve.state = shimExecStateExited
				return err
			}
			log.G(ctx).WithError(err).Error("failed to start container process")
		} else {
			hve.p = p
			hve.pid = hve.p.Process.Pid()
			hve.state = shimExecStateRunning
		}
	}

	// Publish the task/exec start event. This MUST happen before waitForExit to
	// avoid publishing the exit previous to the start.
	if hve.id != hve.tid {
		if err := hve.events.publishEvent(
			ctx,
			runtime.TaskExecStartedEventTopic,
			&eventstypes.TaskExecStarted{
				ContainerID: hve.tid,
				ExecID:      hve.id,
			}); err != nil {
			return err
		}
	} else {
		if err := hve.events.publishEvent(
			ctx,
			runtime.TaskStartEventTopic,
			&eventstypes.TaskStart{
				ContainerID: hve.tid,
			}); err != nil {
			return err
		}
	}

	go hve.waitForExit()

	return nil
}

func (hve *hcsVMExec) Start(ctx context.Context) (err error) {
	// If he.id == he.tid then this is the init exec.
	// We need to initialize the container itself before starting this exec.
	return hve.startInternal(ctx, hve.id == hve.tid)
}

func (hve *hcsVMExec) Kill(ctx context.Context, signal uint32) error {
	hve.sl.Lock()
	defer hve.sl.Unlock()
	switch hve.state {
	case shimExecStateCreated:
		hve.exitFromCreatedL(ctx, 1)
	case shimExecStateRunning:
		if hve.p != nil {
			supported := false
			if osversion.Build() >= osversion.RS5 {
				supported = hve.host.SignalProcessSupported()
			}
			var options interface{}
			var err error
			var opt *guestresource.SignalProcessOptionsWCOW
			opt, err = signals.ValidateWCOW(int(signal), supported)
			if err != nil {
				return fmt.Errorf("signal %d: %v: %s", signal, err, errdefs.ErrFailedPrecondition)
			}
			if opt != nil {
				options = opt
			}
			var delivered bool
			if supported && options != nil {
				go func() {
					signalDelivered, deliveryErr := hve.p.Process.Signal(ctx, options)
					if deliveryErr != nil {
						if !hcs.IsAlreadyStopped(err) {
							log.G(ctx).WithError(deliveryErr).Errorf("error in delivering signal %d, to pid: %d",
								signal, hve.pid)
						}
					}
					if !signalDelivered {
						log.G(ctx).Errorf("error: NotFound; exec: '%s' in task: '%s' not found", hve.id, hve.tid)
					}
				}()
				delivered, err = true, nil
			} else {
				delivered, err = hve.p.Process.Kill(ctx)
			}
			if err != nil {
				if hcs.IsAlreadyStopped(err) {
					return nil
				}
			}
			if !delivered {
				return fmt.Errorf("exec: '%s' in task: '%s' not found: %w", hve.id, hve.tid, errdefs.ErrNotFound)
			}
		} else {
			hve.processDoneOnce.Do(func() { close(hve.processDone) })
		}
	case shimExecStateExited:
		return fmt.Errorf("exec: '%s' in task: '%s' not found: %w", hve.id, hve.tid, errdefs.ErrNotFound)
	default:
		return newExecInvalidStateError(hve.tid, hve.id, hve.state, "kill")
	}
	return nil
}

func (hve *hcsVMExec) ResizePty(_ context.Context, _, _ uint32) error {
	return errors.New("not supported for VM container execs")
}

func (hve *hcsVMExec) CloseIO(ctx context.Context, _ bool) error {
	if hve.p != nil {
		hve.io.CloseStdin(ctx)
	}
	return nil
}

func (hve *hcsVMExec) Wait() *task.StateResponse {
	<-hve.exited
	return hve.Status()
}

func (hve *hcsVMExec) ForceExit(ctx context.Context, status int) {
	hve.sl.Lock()
	defer hve.sl.Unlock()
	if hve.state != shimExecStateExited {
		switch hve.state {
		case shimExecStateCreated:
			hve.exitFromCreatedL(ctx, status)
		case shimExecStateRunning:
			if hve.p != nil {
				_, _ = hve.p.Process.Kill(ctx)
			} else {
				hve.processDoneOnce.Do(func() { close(hve.processDone) })
				// no-op since there's no running process
			}
		}
	}
}

// exitFromCreatedL transitions the shim to the exited state from the created
// state. It is the caller's responsibility to hold `he.sl` for the duration of
// this transition.
//
// This call is idempotent and will not affect any state if the shim is already
// in the `shimExecStateExited` state.
//
// To transition for a created state the following must be done:
//
// 1. Issue `he.processDoneCancel` to unblock the goroutine
// `he.waitForContainerExit()`.
//
// 2. Set `he.state`, `he.exitStatus` and `he.exitedAt` to the exited values.
//
// 3. Release any upstream IO resources that were never used in a copy.
//
// 4. Close `he.exited` channel to unblock any waiters who might have called
// `Create`/`Wait`/`Start` which is a valid pattern.
//
// We DO NOT send the async `TaskExit` event because we never would have sent
// the `TaskStart`/`TaskExecStarted` event.
func (hve *hcsVMExec) exitFromCreatedL(ctx context.Context, status int) {
	if hve.state != shimExecStateExited {
		// Avoid logging the force if we already exited gracefully
		log.G(ctx).WithField("status", status).Debug("hcsVMExec::exitFromCreatedL")

		// Unblock the container exit goroutine
		hve.processDoneOnce.Do(func() { close(hve.processDone) })
		// Transition this exec
		hve.state = shimExecStateExited
		hve.exitStatus = uint32(status)
		hve.exitedAt = time.Now()

		if hve.p != nil {
			hve.io.Close(ctx)
		}
		// Free any waiters
		hve.exitedOnce.Do(func() {
			close(hve.exited)
		})
	}
}

func (hve *hcsVMExec) waitForExit() {
	var err error
	ctx, span := oc.StartSpan(context.Background(), "hcsVMExec::waitForExit")
	defer span.End()
	defer func() { oc.SetSpanStatus(span, err) }()

	var exitCode int

	if hve.p != nil {
		if hve.terminateOnProcessExit {
			log.G(ctx).Debug("process is marked as terminal, waiting")
			err = hve.p.Process.Wait()
			hve.processDoneOnce.Do(func() { close(hve.processDone) })

			var iErr error
			exitCode, iErr = hve.p.Process.ExitCode()
			if iErr != nil {
				log.G(ctx).WithError(iErr).Error("failed to get ExitCode")
			} else {
				log.G(ctx).WithField("exitCode", exitCode).Debug("exited")
			}
		} else {
			if _, err := hve.p.Process.Kill(ctx); err != nil {
				log.G(ctx).WithError(err).Error("failed to init process")
			}
		}
	} else {
		<-hve.processDone
	}

	hve.sl.Lock()
	hve.state = shimExecStateExited
	hve.exitStatus = uint32(exitCode)
	hve.exitedAt = time.Now()
	hve.sl.Unlock()

	if hve.p != nil {
		_ = hve.p.Wait()
		hve.io.Close(ctx)
	}

	// Only send the `runtime.TaskExitEventTopic` notification if this is a true
	// exec. For the `init` exec this is handled in task teardown.
	if hve.tid != hve.id {
		log.G(ctx).Error("unexpected non-init exec of hcsVMExec type")
	}

	// Free any waiters.
	hve.exitedOnce.Do(func() {
		close(hve.exited)
	})
}

// waitForContainerExit waits for `he.c` to exit. Depending on the exec's state
// will forcibly transition this exec to the exited state and unblock any
// waiters.
//
// This MUST be called via a goroutine at exec create.
func (hve *hcsVMExec) waitForContainerExit() {
	ctx, span := oc.StartSpan(context.Background(), "hcsVMExec::waitForContainerExit")
	defer span.End()
	span.AddAttributes(
		trace.StringAttribute("tid", hve.tid),
		trace.StringAttribute("eid", hve.id))

	// wait for container or process to exit and ckean up resrources
	select {
	case <-hve.c.WaitChannel():
		// Container exited first. We need to force the process into the exited
		// state and cleanup any resources
		hve.sl.Lock()
		switch hve.state {
		case shimExecStateCreated:
			hve.exitFromCreatedL(ctx, 1)
		case shimExecStateRunning:
			if hve.p != nil && hve.terminateOnProcessExit {
				_, _ = hve.p.Process.Kill(ctx)
			} else {
				// no-op, since there's no process running
				hve.processDoneOnce.Do(func() { close(hve.processDone) })
			}
		}
		hve.sl.Unlock()
	case <-hve.processDone:
		log.G(ctx).Debug("hcsVMExec waitForContainerExit processDone")
		// Process exited first. This is the normal case do nothing because
		// `he.waitForExit` will release any waiters.
	}
}
