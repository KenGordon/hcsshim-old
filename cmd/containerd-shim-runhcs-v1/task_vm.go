package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	"github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/hcs"
	"github.com/Microsoft/hcsshim/internal/hcs/schema1"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	"github.com/Microsoft/hcsshim/internal/uvm"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type hcsVMTask struct {
	events         publisher
	id             string
	c              cow.Container
	init           shimExec
	host           *uvm.UtilityVM
	ecl            sync.Mutex
	execs          sync.Map
	closed         chan struct{}
	closeOnce      sync.Once
	closeHostOnce  sync.Once
	taskSpec       *specs.Spec
	ioRetryTimeout time.Duration
	resources      *resources.Resources
}

var _ = (shimTask)(&hcsVMTask{})

func newHcsVMTask(
	ctx context.Context,
	events publisher,
	req *task.CreateTaskRequest,
	s *specs.Spec,
) (_ shimTask, err error) {
	owner := filepath.Base(os.Args[0])
	opts, err := oci.SpecToUVMCreateOpts(ctx, s, fmt.Sprintf("%s@vm", req.ID), owner)
	if err != nil {
		return nil, err
	}

	wopts, ok := opts.(*uvm.OptionsWCOW)
	if !ok {
		return nil, fmt.Errorf("options not supported %T: %w", opts, errdefs.ErrFailedPrecondition)
	}

	wopts.BundleDirectory = req.Bundle
	wcow, err := uvm.CreateWCOW(ctx, wopts)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil && wcow != nil {
			wcow.Close()
		}
	}()

	if cs, ok := s.Windows.CredentialSpec.(string); ok {
		log.G(ctx).WithField("CredentialSpec", cs).Debug("creating credential spec")
	}

	caAddr := fmt.Sprintf(uvm.ComputeAgentAddrFmt, req.ID)
	log.G(ctx).WithFields(logrus.Fields{
		"caAddr": caAddr,
		"cid":    req.ID,
	}).Debug("create and assign network setup")
	if err := wcow.CreateAndAssignNetworkSetup(ctx, caAddr, req.ID, true); err != nil {
		return nil, err
	}

	vmWCOW := uvm.NewVMContainer(wcow)
	vmTask := &hcsVMTask{
		id:       req.ID,
		events:   events,
		c:        vmWCOW,
		host:     wcow,
		closed:   make(chan struct{}),
		taskSpec: s,
	}

	log.G(ctx).WithFields(logrus.Fields{
		"stdin":  req.Stdin,
		"stdout": req.Stdout,
		"stderr": req.Stderr,
		"bundle": req.Bundle,
	}).Debug("newHcsVMExec request params")

	var shimOpts *options.Options
	if req.Options != nil {
		v, err := typeurl.UnmarshalAny(req.Options)
		if err != nil {
			return nil, err
		}
		shimOpts = v.(*options.Options)
	}
	var ioRetryTimeout time.Duration
	if shimOpts != nil {
		ioRetryTimeout = time.Duration(shimOpts.IoRetryTimeoutInSec) * time.Second
	}
	io, err := cmd.NewUpstreamIO(ctx, req.ID, req.Stdout, req.Stderr, req.Stdin, req.Terminal, ioRetryTimeout)
	if err != nil {
		return nil, err
	}

	r := resources.NewContainerResources(req.ID)
	// Do we need an init process? probably not?
	vmTask.init = newHcsVmExec(
		ctx,
		events,
		req.ID,
		vmWCOW,
		req.ID,
		req.Bundle,
		s,
		wcow,
		owner,
		io,
		r,
	)

	go vmTask.waitForHostExit()
	go vmTask.waitInitExit()

	// Publish the created event
	if err := vmTask.events.publishEvent(
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
		}); err != nil {
		return nil, err
	}

	return vmTask, nil
}

func (vt *hcsVMTask) ID() string {
	return vt.id
}

func (vt *hcsVMTask) CreateExec(ctx context.Context, req *task.ExecProcessRequest, spec *specs.Process) error {
	vt.ecl.Lock()
	defer vt.ecl.Unlock()

	if _, loaded := vt.execs.Load(req.ExecID); loaded || req.ExecID == "" {
		return fmt.Errorf("exec: '%s' in task: '%s' alread exists: %w", req.ExecID, vt.id, errdefs.ErrAlreadyExists)
	}

	if vt.init.State() != shimExecStateRunning {
		return fmt.Errorf("exec: '' in task: '%s' must be running to create additional execs: %w", vt.id,
			errdefs.ErrFailedPrecondition)
	}

	io, err := cmd.NewUpstreamIO(ctx, req.ID, req.Stdout, req.Stderr, req.Stdin, req.Terminal, vt.ioRetryTimeout)
	if err != nil {
		return err
	}

	spec.User.Username = `NT AUTHORITY\SYSTEM`
	he := newHcsExec(
		ctx,
		vt.events,
		vt.id,
		vt.host,
		vt.c,
		req.ExecID,
		vt.init.Status().Bundle,
		true,
		spec,
		io,
	)

	vt.execs.Store(req.ExecID, he)

	// Publish the created event
	return vt.events.publishEvent(
		ctx,
		runtime.TaskExecAddedEventTopic,
		&eventstypes.TaskExecAdded{
			ContainerID: vt.id,
			ExecID:      req.ExecID,
		},
	)
}

func (vt *hcsVMTask) GetExec(eid string) (shimExec, error) {
	if eid == "" {
		return vt.init, nil
	}
	raw, loaded := vt.execs.Load(eid)
	if !loaded {
		return nil, fmt.Errorf("exec: '%s' in task: '%s' not found: %w", eid, vt.id, errdefs.ErrNotFound)
	}
	return raw.(shimExec), nil
}

func (vt *hcsVMTask) ListExecs() (_ []shimExec, err error) {
	var execs []shimExec
	vt.execs.Range(func(key, value interface{}) bool {
		wt, ok := value.(shimExec)
		if !ok {
			err = fmt.Errorf("failed to load exec %q", key)
			return false
		}
		execs = append(execs, wt)
		return true
	})
	if err != nil {
		return nil, err
	}
	return execs, nil
}

func (vt *hcsVMTask) KillExec(ctx context.Context, eid string, signal uint32, all bool) error {
	e, err := vt.GetExec(eid)
	if err != nil {
		return err
	}
	if all && eid != "" {
		return fmt.Errorf("cannot signal all for non-empty exec: '%s': %w", eid, errdefs.ErrFailedPrecondition)
	}
	if all {
		// We are in a kill all on the init task. Signal everything.
		vt.execs.Range(func(key, value interface{}) bool {
			err := value.(shimExec).Kill(ctx, signal)
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
	if signal == 0x9 && eid == "" && vt.host != nil {
		// If this is a SIGKILL against the init process we start a background
		// timer and wait on either the timer expiring or the process exiting
		// cleanly. If the timer exires first we forcibly close the UVM as we
		// assume the guest is misbehaving for some reason.
		go func() {
			t := time.NewTimer(30 * time.Second)
			execExited := make(chan struct{})
			go func() {
				e.Wait()
				close(execExited)
			}()
			select {
			case <-execExited:
				t.Stop()
			case <-t.C:
				// Safe to call multiple times if called previously on
				// successful shutdown.
				vt.host.Close()
			}
		}()
	}
	return e.Kill(ctx, signal)
}

func (vt *hcsVMTask) DeleteExec(ctx context.Context, eid string) (int, uint32, time.Time, error) {
	e, err := vt.GetExec(eid)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	if eid == "" {
		// We are deleting the init exec. Forcibly exit any additional exec's.
		vt.execs.Range(func(key, value interface{}) bool {
			ex := value.(shimExec)
			if s := ex.State(); s != shimExecStateExited {
				ex.ForceExit(ctx, 1)
			}

			// Iterate all. Returning false stops the iteration. See:
			// https://pkg.go.dev/sync#Map.Range
			return true
		})
	}
	switch state := e.State(); state {
	case shimExecStateCreated:
		e.ForceExit(ctx, 0)
	case shimExecStateRunning:
		return 0, 0, time.Time{}, newExecInvalidStateError(vt.id, eid, state, "delete")
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
			return 0, 0, time.Time{}, fmt.Errorf("waiting for container resource cleanup: %w", hcs.ErrTimeout)
		case <-vt.closed:
		}

		// The init task has now exited. A ForceExit() has already been sent to
		// execs. Cleanup execs and continue.
		vt.execs.Range(func(key, value interface{}) bool {
			if key == "" {
				// Iterate next.
				return true
			}
			vt.execs.Delete(key)

			// Iterate all. Returning false stops the iteration. See:
			// https://pkg.go.dev/sync#Map.Range
			return true
		})

		// cleanup the container directories inside the UVM if required.
		if vt.host != nil {
			if err := vt.host.DeleteContainerState(ctx, vt.id); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to delete container state")
			}
		}
	}

	status := e.Status()
	if eid != "" {
		vt.execs.Delete(eid)
	}

	// Publish the deleted event
	if err := vt.events.publishEvent(
		ctx,
		runtime.TaskDeleteEventTopic,
		&eventstypes.TaskDelete{
			ContainerID: vt.id,
			ID:          eid,
			Pid:         status.Pid,
			ExitStatus:  status.ExitStatus,
			ExitedAt:    status.ExitedAt,
		}); err != nil {
		return 0, 0, time.Time{}, err
	}

	return int(status.Pid), status.ExitStatus, status.ExitedAt.AsTime(), nil
}

func (vt *hcsVMTask) Pids(ctx context.Context) ([]*options.ProcessDetails, error) {
	// Map all user created exec's to pid/exec-id
	pidMap := make(map[int]string)
	vt.execs.Range(func(key, value interface{}) bool {
		ex := value.(shimExec)
		pidMap[ex.Pid()] = ex.ID()

		// Iterate all. Returning false stops the iteration. See:
		// https://pkg.go.dev/sync#Map.Range
		return true
	})
	pidMap[vt.init.Pid()] = vt.init.ID()

	// Get the guest pids
	props, err := vt.c.Properties(ctx, schema1.PropertyTypeProcessList)
	if err != nil {
		if isStatsNotFound(err) {
			return nil, fmt.Errorf("failed to fetch pids: %s: %w", err, errdefs.ErrNotFound)
		}
		return nil, err
	}

	// Copy to pid/exec-id pair's
	pairs := make([]*options.ProcessDetails, len(props.ProcessList))
	for i, p := range props.ProcessList {
		pairs[i] = &options.ProcessDetails{}

		pairs[i].ImageName = p.ImageName
		pairs[i].CreatedAt = timestamppb.New(p.CreateTimestamp)
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

func (vt *hcsVMTask) Wait() *task.StateResponse {
	<-vt.closed
	return vt.init.Wait()
}

func (vt *hcsVMTask) waitInitExit() {
	ctx, span := oc.StartSpan(context.Background(), "hcsTask::waitInitExit")
	defer span.End()
	span.AddAttributes(trace.StringAttribute("tid", vt.id))

	// Wait for it to exit on its own
	vt.init.Wait()

	// Close the host and event the exit
	vt.close(ctx)
}

func (vt *hcsVMTask) ExecInHost(ctx context.Context, req *shimdiag.ExecProcessRequest) (int, error) {
	return 0, errors.New("not implemented")
}

func (vt *hcsVMTask) DumpGuestStacks(ctx context.Context) string {
	return ""
}

func (vt *hcsVMTask) Share(ctx context.Context, req *shimdiag.ShareRequest) error {
	return errors.New("not implemented")
}

func (vt *hcsVMTask) Stats(ctx context.Context) (*stats.Statistics, error) {
	s := &stats.Statistics{}
	vmStats, err := vt.host.Stats(ctx)
	if err != nil && !isStatsNotFound(err) {
		return nil, err
	}
	wcs := &stats.Statistics_Windows{Windows: &stats.WindowsContainerStatistics{}}
	wcs.Windows.Timestamp = timestamppb.New(time.Now())
	wcs.Windows.Processor = &stats.WindowsContainerProcessorStatistics{
		TotalRuntimeNS: vmStats.Processor.TotalRuntimeNS,
	}
	wcs.Windows.Memory = &stats.WindowsContainerMemoryStatistics{
		MemoryUsagePrivateWorkingSetBytes: vmStats.Memory.WorkingSetBytes,
	}
	s.Container = wcs
	log.G(ctx).WithField("stats", fmt.Sprintf("%v", vmStats)).Debug("host stats")
	return s, nil
}

func (vt *hcsVMTask) ProcessorInfo(_ context.Context) (*processorInfo, error) {
	return &processorInfo{
		count: vt.host.ProcessorCount(),
	}, nil
}

func (vt *hcsVMTask) Update(ctx context.Context, req *task.UpdateTaskRequest) error {
	resources, err := typeurl.UnmarshalAny(req.Resources)
	if err != nil {
		return fmt.Errorf("failed to unmarshal resources for container %s update request: %w", req.ID, err)
	}

	if err := verifyTaskUpdateResourcesType(resources); err != nil {
		return err
	}

	return vt.host.Update(ctx, resources, req.Annotations)
}

func (vt *hcsVMTask) waitForHostExit() {
	ctx, span := oc.StartSpan(context.Background(), "hcsVMTask::waitForHostExit")
	defer span.End()
	span.AddAttributes(trace.StringAttribute("tid", vt.id))

	err := vt.host.Wait()
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to wait for host virtual machine exit")
	} else {
		log.G(ctx).Debug("host virtual machine exited")
	}
	vt.execs.Range(func(key, value interface{}) bool {
		ex := value.(shimExec)
		ex.ForceExit(ctx, 1)
		return true
	})
	vt.init.ForceExit(ctx, 1)
	vt.closeHost(ctx)
}

// close shuts down the container that is owned by this task and if
// `ht.ownsHost` will shutdown the hosting VM the container was placed in.
//
// NOTE: For Windows process isolated containers `ht.ownsHost==true && ht.host
// == nil`.
func (vt *hcsVMTask) close(ctx context.Context) {
	vt.closeOnce.Do(func() {
		log.G(ctx).Debug("hcsTask::closeOnce")

		// ht.c should never be nil for a real task but in testing we stub
		// this to avoid a nil dereference. We really should introduce a
		// method or interface for ht.c operations that we can stub for
		// testing.
		if vt.c != nil {
			// Do our best attempt to tear down the container.
			var werr error
			ch := make(chan struct{})
			go func() {
				werr = vt.c.Wait()
				close(ch)
			}()

			if err := resources.ReleaseResources(ctx, vt.resources, vt.host, true); err != nil {
				log.G(ctx).WithError(err).Error("error releasing UVM resources")
			}

			err := vt.c.Shutdown(ctx)
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
					err = hcs.ErrTimeout
					log.G(ctx).WithError(err).Error("failed to wait for container shutdown")
				}
			}

			if err != nil {
				err = vt.c.Terminate(ctx)
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
						log.G(ctx).WithError(hcs.ErrTimeout).Error("failed to wait for container terminate")
					}
				}
			}

			// Close the container handle invalidating all future access.
			if err := vt.c.Close(); err != nil {
				log.G(ctx).WithError(err).Error("failed to close container")
			}
		}
		vt.closeHost(ctx)
	})
}

// closeHost safely closes the hosting UVM if this task is the owner. Once
// closed and all resources released it events the `runtime.TaskExitEventTopic`
// for all upstream listeners.
//
// Note: If this is a process isolated task the hosting UVM is simply a `noop`.
//
// This call is idempotent and safe to call multiple times.
func (vt *hcsVMTask) closeHost(ctx context.Context) {
	vt.closeHostOnce.Do(func() {
		log.G(ctx).Debug("hcsTask::closeHostOnce")

		if vt.host != nil {
			if err := vt.host.Close(); err != nil {
				log.G(ctx).WithError(err).Error("failed host vm shutdown")
			}
		}
		// Send the `init` exec exit notification always.
		exit := vt.init.Status()

		if err := vt.events.publishEvent(
			ctx,
			runtime.TaskExitEventTopic,
			&eventstypes.TaskExit{
				ContainerID: vt.id,
				ID:          exit.ID,
				Pid:         uint32(exit.Pid),
				ExitStatus:  exit.ExitStatus,
				ExitedAt:    exit.ExitedAt,
			}); err != nil {
			log.G(ctx).WithError(err).Error("failed to publish TaskExitEventTopic")
		}
		close(vt.closed)
	})
}
