//go:build !windows
// +build !windows

package main

import (
	"context"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/shim"
	shimservice "github.com/Microsoft/hcsshim/internal/shim"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

type lcolExecState struct {
	m          sync.Mutex
	state      shim.ExecState
	pid        int
	exitStatus uint32
	exitedAt   time.Time
	processCmd *cmd.Cmd
}

func (s *lcolExecState) getPid() int {
	s.m.Lock()
	defer s.m.Unlock()
	return s.pid
}

func (s *lcolExecState) setPid(pid int) {
	s.m.Lock()
	defer s.m.Unlock()
	s.pid = pid
}

func (s *lcolExecState) getExecState() shim.ExecState {
	s.m.Lock()
	defer s.m.Unlock()
	return s.state
}

func (s *lcolExecState) setExecState(state shim.ExecState) {
	s.m.Lock()
	defer s.m.Unlock()
	s.state = state
}

func (s *lcolExecState) getExitStatus() uint32 {
	s.m.Lock()
	defer s.m.Unlock()
	return s.exitStatus
}

func (s *lcolExecState) getExitedAt() time.Time {
	s.m.Lock()
	defer s.m.Unlock()
	return s.exitedAt
}

type lcolExec struct {
	events    events.Publisher
	id        string
	tid       string
	host      *vm.UVM
	container cow.Container
	bundle    string
	spec      *specs.Process
	// TODO katiewasnothere: do we need this upstreamIO thing here?
	io    cmd.UpstreamIO
	state *lcolExecState
}

func (e *lcolExec) ID() string {
	return e.id
}

func (e *lcolExec) Pid() int {
	return e.state.getPid()
}

func (e *lcolExec) State() shim.ExecState {
	return e.state.getExecState()
}

func (e *lcolExec) Status() *task.StateResponse {
	state := e.state.getExecState()
	var s containerd_v1_types.Status
	switch state {
	case shimservice.ExecStateCreated:
		s = containerd_v1_types.StatusCreated
	case shimservice.ExecStateRunning:
		s = containerd_v1_types.StatusRunning
	case shimservice.ExecStateExited:
		s = containerd_v1_types.StatusStopped
	}

	return &task.StateResponse{
		ID:     he.tid,
		ExecID: he.id,
		Bundle: he.bundle,
		Pid:    uint32(e.state.getPid()),
		Status: s,
		// Stdin:      he.io.StdinPath(), katiewasnothere
		// Stdout:     he.io.StdoutPath(),
		// Stderr:     he.io.StderrPath(),
		// Terminal:   he.io.Terminal(),
		ExitStatus: e.state.getExitStatus(),
		ExitedAt:   e.state.getExitedAt(),
	}
}

func (e *lcolExec) Start(ctx context.Context) error {

	// make sure exec is in the correct state

	// if this is an init container exec, issue a start to the underlying container

	// create the command for running the container in the host

	// update the exec's pid

	// update the exec's state to running

	// publish exec started event for non init exec

	// publish task start event otherwise

	// wait for the exec to exit
}

func (e *lcolExec) startInternal(ctx context.Context, initializeContainer bool) (err error) {
	state := e.state.getExecState()
	if state != shimservice.ExecStateCreated {
		return shimservice.NewExecInvalidStateError(e.tid, e.id, state, "start")
	}
	defer func() {
		if err != nil {
			e.exitFromCreated(ctx, 1)
		}
	}()
	if initializeContainer {
		err = e.container.Start(ctx)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				_ = e.container.Terminate(ctx)
				e.container.Close()
			}
		}()
	}
	cmd := &cmd.Cmd{
		Host: e.container,
		// Stdin:  he.io.Stdin(),TODO katiewasnothere
		// Stdout: he.io.Stdout(),
		// Stderr: he.io.Stderr(),
		Log: log.G(ctx).WithFields(logrus.Fields{
			"tid": e.tid,
			"eid": e.id,
		}),
		CopyAfterExitTimeout: time.Second * 1,
	}
	if e.id != e.tid {
		// An init exec passes the process as part of the config. We only pass
		// the spec if this is a true exec.
		cmd.Spec = e.spec
	}
	err = cmd.Start()
	if err != nil {
		return err
	}
	e.process = cmd

	// Assign the PID and transition the state.
	e.state.setPid(e.process.Pid())
	e.state.setExecState(shimservice.ExecStateRunning)

	// Publish the task/exec start event. This MUST happen before waitForExit to
	// avoid publishing the exit previous to the start.
	if e.id != e.tid {
		if err := e.events.Publish(
			ctx,
			runtime.TaskExecStartedEventTopic,
			&eventstypes.TaskExecStarted{
				ContainerID: e.tid,
				ExecID:      e.id,
				Pid:         uint32(he.pid),
			}); err != nil {
			return err
		}
	} else {
		if err := e.events.Publish(
			ctx,
			runtime.TaskStartEventTopic,
			&eventstypes.TaskStart{
				ContainerID: e.tid,
				Pid:         uint32(he.pid),
			}); err != nil {
			return err
		}
	}

	// wait in the background for the exit.
	go e.waitForExit()
	return nil
}

// Kill sends `signal` to this exec process
func (e *lcolExec) Kill(ctx context.Context, signal uint32) error {
	// check the state of the exec
	// if running, send signal to exec process
	state := e.state.getExecState()
	switch state {
	case shimservice.ExecStateCreated:
		e.exitFromCreatedL(ctx, 1)
		return nil
	case shimservice.ExecStateRunning:
		// TODO katiewasnothere: validate the signal sent, check response
		resp, err := e.process.Signal(ctx, signal)
		if err != nil {
			return err
		}
		if !resp {
			// failed to send signal
			return errors.Wrapf(errdefs.ErrNotFound, "exec: '%s' in task: '%s' not found", e.id, e.tid)
		}
		return err
	case shimservice.ExecStateExited:
		return errors.Wrapf(errdefs.ErrNotFound, "exec: '%s' in task: '%s' in exited state", e.id, e.tid)
	default:
		return shimservice.NewExecInvalidStateError(e.tid, e.id, state, "kill")
	}
}

func (e *lcolExec) ResizePty(ctx context.Context, width, height uint32) error {
	// issue command to exec process to resize console

	// TODO katiewasnothere: io
	if !he.io.Terminal() {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "exec: '%s' in task: '%s' is not a tty", he.id, he.tid)
	}

	state := e.state.getExecState()
	if state == shimservice.ExecStateRunning {
		return e.process.ResizeConsole(ctx, uint16(width), uint16(height))
	}
	return nil
}

func (e *lcolExec) CloseIO(ctx context.Context, stdin bool) error {
	// issue command to io to close stdin
	// TODO katiewasnothere: ffix
	e.io.CloseStdin(ctx)
	return nil
}

func (e *lcolExec) Wait() *task.StateResponse {
	// wait for the exec to finish,
	// return the exec status
	<-e.exited
	return e.Status()
}

func (e *lcolExec) ForceExit(ctx context.Context, status int) {
	// check state

	// issue kill to process
	state := e.state.getExecState()
	if state != shimservice.ExecStateExited {
		switch state {
		case shimservice.ExecStateCreated:
			e.exitFromCreated(ctx, status)
		case shimservice.ExecStateRunning:
			// Kill the process to unblock `e.waitForExit`
			_, _ = e.process.Kill(ctx)
		}
	}
}

// exitFromCreatedL transitions the shim to the exited state from the created
// state. It is the callers responsibility to hold `he.sl` for the durration of
// this transition.
//
// This call is idempotent and will not affect any state if the shim is already
// in the `shimExecStateExited` state.
//
// To transition for a created state the following must be done:
//
// 1. Issue `he.processDoneCancel` to unblock the goroutine
// `he.waitForContainerExit()``.
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
func (e *lcolExec) exitFromCreated(ctx context.Context, status int) {
	state := e.state.getExecState()
	if state != shimservice.ExecStateExited {
		// Avoid logging the force if we already exited gracefully
		log.G(ctx).WithField("status", status).Debug("hcsExec::exitFromCreatedL")

		// Unblock the container exit goroutine
		he.processDoneOnce.Do(func() { close(he.processDone) })
		// Transition this exec
		he.state = shimservice.ExecStateExited
		he.exitStatus = uint32(status)
		he.exitedAt = time.Now()
		// Release all upstream IO connections (if any)
		he.io.Close(ctx)
		// Free any waiters
		he.exitedOnce.Do(func() {
			close(he.exited)
		})
	}
}

// waitForContainerExit waits for `he.c` to exit. Depending on the exec's state
// will forcibly transition this exec to the exited state and unblock any
// waiters.
//
// This MUST be called via a goroutine at exec create.
func (e *lcolExec) waitForContainerExit() {
	ctx, span := trace.StartSpan(context.Background(), "hcsExec::waitForContainerExit")
	defer span.End()
	span.AddAttributes(
		trace.StringAttribute("tid", e.tid),
		trace.StringAttribute("eid", e.id))

	cexit := make(chan struct{})
	go func() {
		_ = e.container.Wait()
		close(cexit)
	}()
	select {
	case <-cexit:
		// Container exited first. We need to force the process into the exited
		// state and cleanup any resources
		state := e.state.getExecState()
		switch state {
		case shimservice.ExecStateCreated:
			e.exitFromCreated(ctx, 1)
		case shimservice.ExecStateRunning:
			// Kill the process to unblock `he.waitForExit`.
			_, _ = e.process.Kill(ctx)
		}
	case <-he.processDone:
		// Process exited first. This is the normal case do nothing because
		// `he.waitForExit` will release any waiters.
	}
}

// waitForExit waits for the `he.p` to exit. This MUST only be called after a
// successful call to `Create` and MUST not be called more than once.
//
// This MUST be called via a goroutine.
//
// In the case of an exit from a running process the following must be done:
//
// 1. Wait for `he.p` to exit.
//
// 2. Issue `he.processDoneCancel` to unblock the goroutine
// `he.waitForContainerExit()` (if still running). We do this early to avoid the
// container exit also attempting to kill the process. However this race
// condition is safe and handled.
//
// 3. Capture the process exit code and set `he.state`, `he.exitStatus` and
// `he.exitedAt` to the exited values.
//
// 4. Wait for all IO to complete and release any upstream IO connections.
//
// 5. Send the async `TaskExit` to upstream listeners of any events.
//
// 6. Close `he.exited` channel to unblock any waiters who might have called
// `Create`/`Wait`/`Start` which is a valid pattern.
//
// 7. Finally, save the UVM and this container as a template if specified.
func (e *lcolExec) waitForExit() {
	ctx, span := trace.StartSpan(context.Background(), "hcsExec::waitForExit")
	defer span.End()
	span.AddAttributes(
		trace.StringAttribute("tid", he.tid),
		trace.StringAttribute("eid", he.id))

	// waits for the cmd.Cmd process to exit

	err := he.p.Process.Wait()
	if err != nil {
		log.G(ctx).WithError(err).Error("failed process Wait")
	}

	// Issue the process cancellation to unblock the container wait as early as
	// possible.
	he.processDoneOnce.Do(func() { close(he.processDone) })

	code, err := he.p.Process.ExitCode()
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to get ExitCode")
	} else {
		log.G(ctx).WithField("exitCode", code).Debug("exited")
	}

	he.sl.Lock()
	he.state = shimservice.ExecStateExited
	he.exitStatus = uint32(code)
	he.exitedAt = time.Now()
	he.sl.Unlock()

	// Wait for all IO copies to complete and free the resources.
	_ = he.p.Wait()
	he.io.Close(ctx)

	// Only send the `runtime.TaskExitEventTopic` notification if this is a true
	// exec. For the `init` exec this is handled in task teardown.
	if he.tid != he.id {
		// We had a valid process so send the exited notification.
		if err := he.events.Publish(
			ctx,
			runtime.TaskExitEventTopic,
			&eventstypes.TaskExit{
				ContainerID: he.tid,
				ID:          he.id,
				Pid:         uint32(he.pid),
				ExitStatus:  he.exitStatus,
				ExitedAt:    he.exitedAt,
			}); err != nil {
			log.G(ctx).WithError(err).Error("failed to publish TaskExitEvent")
		}
	}

	// Free any waiters.
	he.exitedOnce.Do(func() {
		close(he.exited)
	})
}
