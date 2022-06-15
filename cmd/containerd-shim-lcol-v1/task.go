//go:build !windows
// +build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim/internal/cmd/io"
	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/hcs/schema1"
	"github.com/Microsoft/hcsshim/internal/hvlitevm"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/shim"
	shimservice "github.com/Microsoft/hcsshim/internal/shim"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	"github.com/Microsoft/hcsshim/pkg/service/options"
	runhcsopts "github.com/Microsoft/hcsshim/pkg/service/options"
	"github.com/Microsoft/hcsshim/pkg/service/stats"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/typeurl"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

type lcolTask struct {
	events    events.Publisher
	id        string
	container cow.Container
	ownsHost  bool
	// TODO katiewasnothere: this probably needs to be fixed to
	// build on linux
	// containerResources *resources.Resources 
	host *hvlitevm.UtilityVM

	// init is the init process of the container.
	//
	// Note: the invariant `container state == init.State()` MUST be true. IE:
	// if the init process exits the container as a whole and all exec's MUST
	// exit.
	//
	// It MUST be treated as read only in the lifetime of the task.
	init shimservice.Exec

	ecl   sync.Mutex
	execs sync.Map

	closed    chan struct{}
	closeOnce sync.Once
	// closeHostOnce is used to close `host`. This will only be used if
	// `ownsHost==true` and `host != nil`.
	closeHostOnce sync.Once

	// taskSpec represents the spec/configuration for this task.
	taskSpec       *specs.Spec
	ioRetryTimeout time.Duration
}

var _ = (shim.Task)(&lcolTask{})

func newLCOLTask(
	ctx context.Context,
	events events.Publisher,
	parent *hvlitevm.UtilityVM,
	ownsParent bool,
	req *task.CreateTaskRequest,
	s *specs.Spec) (_ shimservice.Task, err error) {
	log.G(ctx).WithFields(logrus.Fields{
		"tid":        req.ID,
		"ownsParent": ownsParent,
	}).Debug("newHcsTask")

	owner := filepath.Base(os.Args[0])

	var shimOpts *runhcsopts.Options
	if req.Options != nil {
		v, err := typeurl.UnmarshalAny(req.Options)
		if err != nil {
			return nil, err
		}
		shimOpts = v.(*runhcsopts.Options)
	}

	var ioRetryTimeout time.Duration
	if shimOpts != nil {
		ioRetryTimeout = time.Duration(shimOpts.IoRetryTimeoutInSec) * time.Second
	}
	io, err := io.NewUpstreamIO(ctx, req.ID, req.Stdout, req.Stderr, req.Stdin, req.Terminal, ioRetryTimeout)
	if err != nil {
		return nil, err
	}

	container, err := createContainer(ctx, req.ID, owner, "", s, parent, shimOpts)
	if err != nil {
		return nil, err
	}

	t := &lcolTask{
		events:         events,
		id:             req.ID,
		container:      container,
		ownsHost:       ownsParent,
		host:           parent,
		closed:         make(chan struct{}),
		taskSpec:       s,
		ioRetryTimeout: ioRetryTimeout,
	}
	t.init = newLCOLExec(
		ctx,
		events,
		req.ID,
		parent,
		container,
		req.ID,
		req.Bundle,
		s.Process,
		io,
	)

	if parent != nil {
		// We have a parent UVM. Listen for its exit and forcibly close this
		// task. This is not expected but in the event of a UVM crash we need to
		// handle this case.
		go t.waitForHostExit()
	}

	// In the normal case the `Signal` call from the caller killed this task's
	// init process. Or the init process ran to completion - this will mostly
	// happen when we are creating a template and want to wait for init process
	// to finish before we save the template. In such cases do not tear down the
	// container after init exits - because we need the container in the template
	go t.waitInitExit(true)

	// Publish the created event
	if err := t.events.Publish(
		ctx,
		runtime.TaskCreateEventTopic,
		&eventstypes.TaskCreate{
			ContainerID: req.ID,
			Bundle:      req.Bundle,
			Rootfs:      req.Rootfs,
			IO: &eventstypes.TaskIO{
				Stdin:    req.Stdin,
				Stdout:   req.Stdout,
				Stderr:   req.Stderr,
				Terminal: req.Terminal,
			},
			Checkpoint: "",
			Pid:        uint32(t.init.Pid()),
		}); err != nil {
		return nil, err
	}
	return t, nil
}

// createContainer is a generic call to return either a process/hypervisor isolated container, or a job container
//  based on what is set in the OCI spec.
func createContainer(ctx context.Context, id, owner, netNS string, s *specs.Spec, parent *hvlitevm.UtilityVM, shimOpts *runhcsopts.Options) (cow.Container, error) {
	return parent.CreateContainer(ctx, id, s)
}

// ID returns the id of the task
func (t *lcolTask) ID() string {
	return t.id
}

// CreateExec creates a new exec within this task
func (t *lcolTask) CreateExec(ctx context.Context, req *task.ExecProcessRequest, s *specs.Process) error {
	t.ecl.Lock()
	defer t.ecl.Unlock()

	// If the task exists or we got a request for "" which is the init task
	// fail.
	if _, loaded := t.execs.Load(req.ExecID); loaded || req.ExecID == "" {
		return errors.Wrapf(errdefs.ErrAlreadyExists, "exec: '%s' in task: '%s' already exists", req.ExecID, t.id)
	}

	if t.init.State() != shimservice.ExecStateRunning {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "exec: '' in task: '%s' must be running to create additional execs", t.id)
	}

	io, err := io.NewUpstreamIO(ctx, req.ID, req.Stdout, req.Stderr, req.Stdin, req.Terminal, t.ioRetryTimeout)
	if err != nil {
		return err
	}

	e := newLCOLExec(
		ctx,
		t.events,
		t.id,
		t.host,
		t.container,
		req.ExecID,
		t.init.Status().Bundle,
		s,
		io,
	)

	t.execs.Store(req.ExecID, e)

	// Publish the created event
	return t.events.Publish(
		ctx,
		runtime.TaskExecAddedEventTopic,
		&eventstypes.TaskExecAdded{
			ContainerID: t.id,
			ExecID:      req.ExecID,
		})
}

// GetExec returns an exec in this task with the id `eid`
func (t *lcolTask) GetExec(eid string) (shim.Exec, error) {
	if eid == "" {
		return t.init, nil
	}
	raw, loaded := t.execs.Load(eid)
	if !loaded {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "exec: '%s' in task: '%s' not found", eid, t.id)
	}
	return raw.(shimservice.Exec), nil
}

// KillExec sends `signal` to exec with id `eid`
func (t *lcolTask) KillExec(ctx context.Context, eid string, signal uint32, all bool) error {
	e, err := t.GetExec(eid)
	if err != nil {
		return err
	}
	if all && eid != "" {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "cannot signal all for non-empty exec: '%s'", eid)
	}
	if all {
		// We are in a kill all on the init task. Signal everything.
		t.execs.Range(func(key, value interface{}) bool {
			err := value.(shimservice.Exec).Kill(ctx, signal)
			if err != nil {
				log.G(ctx).WithFields(logrus.Fields{
					"eid":           key,
					logrus.ErrorKey: err,
				}).Warn("failed to kill exec in task")
			}

			// Iterate all. Returning false stops the iteration. See:
			// https://pkg.go.dev/sync#Map.Range
			return true
		})
	}
	if signal == 0x9 && eid == "" && t.host != nil {
		// If this is a SIGKILL against the init process we start a background
		// timer and wait on either the timer expiring or the process exiting
		// cleanly. If the timer exires first we forcibly close the UVM as we
		// assume the guest is misbehaving for some reason.
		go func() {
			timer := time.NewTimer(30 * time.Second)
			execExited := make(chan struct{})
			go func() {
				e.Wait()
				close(execExited)
			}()
			select {
			case <-execExited:
				timer.Stop()
			case <-timer.C:
				// Safe to call multiple times if called previously on
				// successful shutdown.
				t.host.Close()
			}
		}()
	}
	return e.Kill(ctx, signal)
}

// DeleteExec deletes an exec with id `eid` in this task
func (t *lcolTask) DeleteExec(ctx context.Context, eid string) (int, uint32, time.Time, error) {
	e, err := t.GetExec(eid)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	if eid == "" {
		// We are deleting the init exec. Forcibly exit any additional exec's.
		t.execs.Range(func(key, value interface{}) bool {
			ex := value.(shimservice.Exec)
			if s := ex.State(); s != shimservice.ExecStateExited {
				ex.ForceExit(ctx, 1)
			}

			// Iterate all. Returning false stops the iteration. See:
			// https://pkg.go.dev/sync#Map.Range
			return true
		})
	}
	switch state := e.State(); state {
	case shimservice.ExecStateCreated:
		e.ForceExit(ctx, 0)
	case shimservice.ExecStateRunning:
		return 0, 0, time.Time{}, shimservice.NewExecInvalidStateError(t.id, eid, state, "delete")
	}

	if eid == "" {
		// We are killing the init task, so we expect the container to be
		// stopped after this.
		//
		// The task process may have already exited, and the status set to
		// shimExecStateExited, but resources may still be in the process
		// of being cleaned up. Wait for ht.closed to be closed. This signals
		// that waitInitExit() has finished destroying container resources,
		// and layers were umounted.
		// If the shim exits before resources are cleaned up, those resources
		// will remain locked and untracked, which leads to lingering sandboxes
		// and container resources like base vhdx.
		select {
		case <-time.After(30 * time.Second):
			log.G(ctx).Error("timed out waiting for resource cleanup")
			return 0, 0, time.Time{}, errors.New("waiting for container resource cleanup")
		case <-t.closed:
		}

		// The init task has now exited. A ForceExit() has already been sent to
		// execs. Cleanup execs and continue.
		t.execs.Range(func(key, value interface{}) bool {
			if key == "" {
				// Iterate next.
				return true
			}
			t.execs.Delete(key)

			// Iterate all. Returning false stops the iteration. See:
			// https://pkg.go.dev/sync#Map.Range
			return true
		})
	}

	status := e.Status()
	if eid != "" {
		t.execs.Delete(eid)
	}

	// Publish the deleted event
	if err := t.events.Publish(
		ctx,
		runtime.TaskDeleteEventTopic,
		&eventstypes.TaskDelete{
			ContainerID: t.id,
			ID:          eid,
			Pid:         status.Pid,
			ExitStatus:  status.ExitStatus,
			ExitedAt:    status.ExitedAt,
		}); err != nil {
		return 0, 0, time.Time{}, err
	}

	return int(status.Pid), status.ExitStatus, status.ExitedAt, nil
}

// Pids returns all process pids in this task
func (t *lcolTask) Pids(ctx context.Context) ([]options.ProcessDetails, error) {
	// Map all user created exec's to pid/exec-id
	pidMap := make(map[int]string)
	t.execs.Range(func(key, value interface{}) bool {
		ex := value.(shimservice.Exec)
		pidMap[ex.Pid()] = ex.ID()

		// Iterate all. Returning false stops the iteration. See:
		// https://pkg.go.dev/sync#Map.Range
		return true
	})
	pidMap[t.init.Pid()] = t.init.ID()

	// Get the guest pids
	props, err := t.container.Properties(ctx, schema1.PropertyTypeProcessList)
	if err != nil {
		return nil, err
	}

	// Copy to pid/exec-id pair's
	pairs := make([]runhcsopts.ProcessDetails, len(props.ProcessList))
	for i, p := range props.ProcessList {
		pairs[i].ImageName = p.ImageName
		pairs[i].CreatedAt = p.CreateTimestamp
		pairs[i].KernelTime_100Ns = p.KernelTime100ns
		pairs[i].MemoryCommitBytes = p.MemoryCommitBytes
		pairs[i].MemoryWorkingSetPrivateBytes = p.MemoryWorkingSetPrivateBytes
		pairs[i].MemoryWorkingSetSharedBytes = p.MemoryWorkingSetSharedBytes
		pairs[i].ProcessID = p.ProcessId
		pairs[i].UserTime_100Ns = p.KernelTime100ns

		if eid, ok := pidMap[int(p.ProcessId)]; ok {
			pairs[i].ExecID = eid
		}
	}
	return pairs, nil
}

// Wait for init task to complete
func (t *lcolTask) Wait() *task.StateResponse {
	<-t.closed
	return t.init.Wait()
}

// ExecInHost executes a process in the host of this task
func (t *lcolTask) ExecInHost(ctx context.Context, req *shimdiag.ExecProcessRequest) (int, error) {
	return 0, errdefs.ErrNotImplemented
}

func (t *lcolTask) waitForHostExit() {
	ctx, span := trace.StartSpan(context.Background(), "hcsTask::waitForHostExit")
	defer span.End()
	span.AddAttributes(trace.StringAttribute("tid", t.id))

	err := t.host.Wait()
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to wait for host virtual machine exit")
	} else {
		log.G(ctx).Debug("host virtual machine exited")
	}

	t.execs.Range(func(key, value interface{}) bool {
		ex := value.(shimservice.Exec)
		ex.ForceExit(ctx, 1)

		// Iterate all. Returning false stops the iteration. See:
		// https://pkg.go.dev/sync#Map.Range
		return true
	})
	t.init.ForceExit(ctx, 1)
	t.closeHost(ctx)
}

func (t *lcolTask) close(ctx context.Context) {
	t.closeOnce.Do(func() {
		log.G(ctx).Debug("hcsTask::closeOnce")

		// ht.c should never be nil for a real task but in testing we stub
		// this to avoid a nil dereference. We really should introduce a
		// method or interface for ht.c operations that we can stub for
		// testing.
		if t.container != nil {
			// Do our best attempt to tear down the container.
			var werr error
			ch := make(chan struct{})
			go func() {
				werr = t.container.Wait()
				close(ch)
			}()
			err := t.container.Shutdown(ctx)
			if err != nil {
				log.G(ctx).WithError(err).Error("failed to shutdown container")
			} else {
				t := time.NewTimer(time.Second * 30)
				select {
				case <-ch:
					err = werr
					t.Stop()
					if err != nil {
						log.G(ctx).WithError(err).Error("failed to wait for container shutdown")
					}
				case <-t.C:
					log.G(ctx).Error("failed to wait for container shutdown")
				}
			}

			if err != nil {
				err = t.container.Terminate(ctx)
				if err != nil {
					log.G(ctx).WithError(err).Error("failed to terminate container")
				} else {
					t := time.NewTimer(time.Second * 30)
					select {
					case <-ch:
						err = werr
						t.Stop()
						if err != nil {
							log.G(ctx).WithError(err).Error("failed to wait for container terminate")
						}
					case <-t.C:
						log.G(ctx).Error("failed to wait for container terminate")
					}
				}
			}

			// Release any resources associated with the container.
			/*if err := resources.ReleaseResources(ctx, t.containerResources, t.host, true); err != nil {
				log.G(ctx).WithError(err).Error("failed to release container resources")
			}*/

			// Close the container handle invalidating all future access.
			if err := t.container.Close(); err != nil {
				log.G(ctx).WithError(err).Error("failed to close container")
			}
		}
		t.closeHost(ctx)
	})
}

func (t *lcolTask) closeHost(ctx context.Context) {
	t.closeHostOnce.Do(func() {
		log.G(ctx).Debug("hcsTask::closeHostOnce")

		if t.ownsHost && t.host != nil {
			if err := t.host.Close(); err != nil {
				log.G(ctx).WithError(err).Error("failed host vm shutdown")
			}
		}
		// Send the `init` exec exit notification always.
		exit := t.init.Status()

		if err := t.events.Publish(
			ctx,
			runtime.TaskExitEventTopic,
			&eventstypes.TaskExit{
				ContainerID: t.id,
				ID:          exit.ID,
				Pid:         uint32(exit.Pid),
				ExitStatus:  exit.ExitStatus,
				ExitedAt:    exit.ExitedAt,
			}); err != nil {
			log.G(ctx).WithError(err).Error("failed to publish TaskExitEventTopic")
		}
		close(t.closed)
	})
}

func (t *lcolTask) waitInitExit(destroyContainer bool) {
	ctx, span := trace.StartSpan(context.Background(), "hcsTask::waitInitExit")
	defer span.End()
	span.AddAttributes(trace.StringAttribute("tid", t.id))

	// Wait for it to exit on its own
	t.init.Wait()

	if destroyContainer {
		// Close the host and event the exit
		t.close(ctx)
	} else {
		// Close the container's host, but do not close or terminate the container itself
		t.closeHost(ctx)
	}
}

// DumpGuestStacks dumps the GCS stacks associated with this task
func (t *lcolTask) DumpGuestStacks(ctx context.Context) string {
	// not implemented
	return ""
}

// Share shares a directory/file into the task host
func (t *lcolTask) Share(ctx context.Context, req *shimdiag.ShareRequest) error {
	return errdefs.ErrNotImplemented
}

// Stats returns metrics for the task
func (t *lcolTask) Stats(ctx context.Context) (*stats.Statistics, error) {
	return nil, errdefs.ErrNotImplemented
}

// ProcessInfo returns information on the task's processor settings
func (t *lcolTask) ProcessorInfo(ctx context.Context) (*shim.ProcessorInfo, error) {
	return nil, errdefs.ErrNotImplemented
}

// Update updates a task's container settings/resources
func (t *lcolTask) Update(ctx context.Context, req *task.UpdateTaskRequest) error {
	return errdefs.ErrNotImplemented
}
