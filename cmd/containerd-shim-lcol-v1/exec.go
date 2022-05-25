//go:build !windows
// +build !windows

package main

import (
	"context"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/cmd/io"
	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/hvlitevm"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/shim"
	shimservice "github.com/Microsoft/hcsshim/internal/shim"
	eventstypes "github.com/containerd/containerd/api/events"
	containerd_v1_types "github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

type lcolExec struct {
	events    events.Publisher
	id        string
	tid       string
	host      *hvlitevm.UtilityVM
	container cow.Container
	bundle    string
	spec      *specs.Process
	io        io.UpstreamIO

	m          sync.Mutex
	state      shim.ExecState
	pid        int
	exitStatus uint32
	exitedAt   time.Time
	p          *cmd.Cmd

	processDone     chan struct{}
	processDoneOnce sync.Once

	exited     chan struct{}
	exitedOnce sync.Once
}

var _ = (shim.Exec)(&lcolExec{})

func newLCOLExec(
	ctx context.Context,
	events events.Publisher,
	tid string,
	host *hvlitevm.UtilityVM,
	c cow.Container,
	id, bundle string,
	spec *specs.Process,
	io io.UpstreamIO) shimservice.Exec {
	log.G(ctx).WithFields(logrus.Fields{
		"tid":    tid,
		"eid":    id, // Init exec ID is always same as Task ID
		"bundle": bundle,
	}).Debug("newHcsExec")

	he := &lcolExec{
		events:      events,
		tid:         tid,
		host:        host,
		container:   c,
		id:          id,
		bundle:      bundle,
		spec:        spec,
		io:          io,
		processDone: make(chan struct{}),
		state:       shimservice.ExecStateCreated,
		exitStatus:  255, // By design for non-exited process status.
		exited:      make(chan struct{}),
	}
	go he.waitForContainerExit()
	return he
}

func (e *lcolExec) ID() string {
	return e.id
}

func (e *lcolExec) Pid() int {
	e.m.Lock()
	defer e.m.Unlock()
	return e.pid
}

func (e *lcolExec) State() shim.ExecState {
	e.m.Lock()
	defer e.m.Unlock()
	return e.state
}

func (e *lcolExec) Status() *task.StateResponse {
	e.m.Lock()
	defer e.m.Unlock()

	var s containerd_v1_types.Status
	switch e.state {
	case shimservice.ExecStateCreated:
		s = containerd_v1_types.StatusCreated
	case shimservice.ExecStateRunning:
		s = containerd_v1_types.StatusRunning
	case shimservice.ExecStateExited:
		s = containerd_v1_types.StatusStopped
	}

	return &task.StateResponse{
		ID:         e.tid,
		ExecID:     e.id,
		Bundle:     e.bundle,
		Pid:        uint32(e.pid),
		Status:     s,
		Stdin:      e.io.StdinPath(),
		Stdout:     e.io.StdoutPath(),
		Stderr:     e.io.StderrPath(),
		Terminal:   e.io.Terminal(),
		ExitStatus: e.exitStatus,
		ExitedAt:   e.exitedAt,
	}
}

func (e *lcolExec) Start(ctx context.Context) error {
	return e.startInternal(ctx, e.id == e.tid)
}

func (e *lcolExec) startInternal(ctx context.Context, initializeContainer bool) (err error) {
	e.m.Lock()
	defer e.m.Unlock()

	if e.state != shimservice.ExecStateCreated {
		return shimservice.NewExecInvalidStateError(e.tid, e.id, e.state, "start")
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
		Host:   e.container,
		Stdin:  e.io.Stdin(),
		Stdout: e.io.Stdout(),
		Stderr: e.io.Stderr(),
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
	e.p = cmd

	// Assign the PID and transition the state.
	e.pid = e.p.Process.Pid()
	e.state = shimservice.ExecStateRunning

	// Publish the task/exec start event. This MUST happen before waitForExit to
	// avoid publishing the exit previous to the start.
	if e.id != e.tid {
		if err := e.events.Publish(
			ctx,
			runtime.TaskExecStartedEventTopic,
			&eventstypes.TaskExecStarted{
				ContainerID: e.tid,
				ExecID:      e.id,
				Pid:         uint32(e.pid),
			}); err != nil {
			return err
		}
	} else {
		if err := e.events.Publish(
			ctx,
			runtime.TaskStartEventTopic,
			&eventstypes.TaskStart{
				ContainerID: e.tid,
				Pid:         uint32(e.pid),
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
	e.m.Lock()
	defer e.m.Unlock()
	// check the state of the exec
	// if running, send signal to exec process
	switch e.state {
	case shimservice.ExecStateCreated:
		e.exitFromCreated(ctx, 1)
		return nil
	case shimservice.ExecStateRunning:
		// TODO katiewasnothere: validate the signal sent, check response
		resp, err := e.p.Process.Signal(ctx, signal)
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
		return shimservice.NewExecInvalidStateError(e.tid, e.id, e.state, "kill")
	}
}

func (e *lcolExec) ResizePty(ctx context.Context, width, height uint32) error {
	// issue command to exec process to resize console
	if !e.io.Terminal() {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "exec: '%s' in task: '%s' is not a tty", e.id, e.tid)
	}

	if e.state == shimservice.ExecStateRunning {
		return e.p.Process.ResizeConsole(ctx, uint16(width), uint16(height))
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
	e.m.Lock()
	defer e.m.Unlock()

	// issue kill to process
	if e.state != shimservice.ExecStateExited {
		switch e.state {
		case shimservice.ExecStateCreated:
			e.exitFromCreated(ctx, status)
		case shimservice.ExecStateRunning:
			// Kill the process to unblock `e.waitForExit`
			_, _ = e.p.Process.Kill(ctx)
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
	e.m.Lock()
	defer e.m.Unlock()
	if e.state != shimservice.ExecStateExited {
		// Avoid logging the force if we already exited gracefully
		log.G(ctx).WithField("status", status).Debug("hcsExec::exitFromCreatedL")

		// Unblock the container exit goroutine
		e.processDoneOnce.Do(func() { close(e.processDone) })
		// Transition this exec
		e.state = shimservice.ExecStateExited
		e.exitStatus = uint32(status)
		e.exitedAt = time.Now()
		// Release all upstream IO connections (if any)
		e.io.Close(ctx)
		// Free any waiters
		e.exitedOnce.Do(func() {
			close(e.exited)
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
		e.m.Lock()
		switch e.state {
		case shimservice.ExecStateCreated:
			e.exitFromCreated(ctx, 1)
		case shimservice.ExecStateRunning:
			// Kill the process to unblock `he.waitForExit`.
			_, _ = e.p.Process.Kill(ctx)
		}
		e.m.Unlock()
	case <-e.processDone:
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
		trace.StringAttribute("tid", e.tid),
		trace.StringAttribute("eid", e.id))

	// waits for the cmd.Cmd process to exit

	err := e.p.Process.Wait()
	if err != nil {
		log.G(ctx).WithError(err).Error("failed process Wait")
	}

	// Issue the process cancellation to unblock the container wait as early as
	// possible.
	e.processDoneOnce.Do(func() { close(e.processDone) })

	code, err := e.p.Process.ExitCode()
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to get ExitCode")
	} else {
		log.G(ctx).WithField("exitCode", code).Debug("exited")
	}

	e.m.Lock()
	e.state = shimservice.ExecStateExited
	e.exitStatus = uint32(code)
	e.exitedAt = time.Now()
	e.m.Unlock()

	// Wait for all IO copies to complete and free the resources.
	_ = e.p.Wait()
	e.io.Close(ctx)

	// Only send the `runtime.TaskExitEventTopic` notification if this is a true
	// exec. For the `init` exec this is handled in task teardown.
	if e.tid != e.id {
		// We had a valid process so send the exited notification.
		if err := e.events.Publish(
			ctx,
			runtime.TaskExitEventTopic,
			&eventstypes.TaskExit{
				ContainerID: e.tid,
				ID:          e.id,
				Pid:         uint32(e.pid),
				ExitStatus:  e.exitStatus,
				ExitedAt:    e.exitedAt,
			}); err != nil {
			log.G(ctx).WithError(err).Error("failed to publish TaskExitEvent")
		}
	}

	// Free any waiters.
	e.exitedOnce.Do(func() {
		close(e.exited)
	})
}
