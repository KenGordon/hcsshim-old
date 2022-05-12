package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Microsoft/hcsshim/internal/extendedtask"
	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/internal/shim"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	runhcsopts "github.com/Microsoft/hcsshim/pkg/service/options"
	containerd_v1_types "github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/typeurl"
	google_protobuf1 "github.com/gogo/protobuf/types"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

var empty = &google_protobuf1.Empty{}

// getPod returns the pod this shim is tracking or else returns `nil`. It is the
// callers responsibility to verify that `s.isSandbox == true` before calling
// this method.
//
//
// If `pod==nil` returns `errdefs.ErrFailedPrecondition`.
func (s *Service) getPod() (shim.Pod, error) {
	raw := s.taskOrPod.Load()
	if raw == nil {
		return nil, errors.Wrapf(errdefs.ErrFailedPrecondition, "task with id: '%s' must be created first", s.tid)
	}
	return raw.(shim.Pod), nil
}

// getTask returns a task matching `tid` or else returns `nil`. This properly
// handles a task in a pod or a singular task shim.
//
// If `tid` is not found will return `errdefs.ErrNotFound`.
func (s *Service) getTask(tid string) (shim.Task, error) {
	raw := s.taskOrPod.Load()
	if raw == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "task with id: '%s' not found", tid)
	}
	if s.isSandbox {
		p := raw.(shim.Pod)
		return p.GetTask(tid)
	}
	// When its not a sandbox only the init task is a valid id.
	if s.tid != tid {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "task with id: '%s' not found", tid)
	}
	return raw.(shim.Task), nil
}

func (s *Service) stateInternal(ctx context.Context, req *task.StateRequest) (*task.StateResponse, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	e, err := t.GetExec(req.ExecID)
	if err != nil {
		return nil, err
	}
	return e.Status(), nil
}

func (s *Service) createInternal(ctx context.Context, req *task.CreateTaskRequest) (*task.CreateTaskResponse, error) {
	// TODO katiewasnothere: fix this
	// setupDebuggerEvent()

	var shimOpts *runhcsopts.Options
	if req.Options != nil {
		v, err := typeurl.UnmarshalAny(req.Options)
		if err != nil {
			return nil, err
		}
		shimOpts = v.(*runhcsopts.Options)
	}

	var spec specs.Spec
	f, err := os.Open(filepath.Join(req.Bundle, "config.json"))
	if err != nil {
		return nil, err
	}
	if err := json.NewDecoder(f).Decode(&spec); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()

	spec = oci.UpdateSpecFromOptions(spec, shimOpts)
	//expand annotations after defaults have been loaded in from options
	err = oci.ProcessAnnotations(ctx, &spec)
	// since annotation expansion is used to toggle security features
	// raise it rather than suppress and move on
	if err != nil {
		return nil, errors.Wrap(err, "unable to process OCI Spec annotations")
	}

	if err := oci.UpdateSpecWithLayerPaths(&spec, req.Rootfs); err != nil {
		return nil, errors.Wrap(err, "failed to construct parent layer paths")
	}

	if req.Terminal && req.Stderr != "" {
		return nil, errors.Wrap(errdefs.ErrFailedPrecondition, "if using terminal, stderr must be empty")
	}

	resp := &task.CreateTaskResponse{}
	s.cl.Lock()
	if s.isSandbox {
		pod, err := s.getPod()
		if err == nil {
			// The POD sandbox was previously created. Unlock and forward to the POD
			s.cl.Unlock()
			t, err := pod.CreateTask(ctx, req, &spec)
			if err != nil {
				return nil, err
			}
			e, _ := t.GetExec("")
			resp.Pid = uint32(e.Pid())
			return resp, nil
		}
		// TODO katiewasnothere: figure out how to properly create a new pod depending on what we're running
		pod, err = s.podFactory.Create(ctx, s.events, req, &spec)
		if err != nil {
			s.cl.Unlock()
			return nil, err
		}
		t, _ := pod.GetTask(req.ID)
		e, _ := t.GetExec("")
		resp.Pid = uint32(e.Pid())
		s.taskOrPod.Store(pod)
	} else {
		// TODO katiewasnothere: figure out what to do with this
		// figure out what happens if this is not set
		t, err := s.standaloneTaskFactory.Create(ctx, s.events, req, &spec)
		if err != nil {
			s.cl.Unlock()
			return nil, err
		}
		e, _ := t.GetExec("")
		resp.Pid = uint32(e.Pid())
		s.taskOrPod.Store(t)
	}
	s.cl.Unlock()
	return resp, nil
}

func (s *Service) startInternal(ctx context.Context, req *task.StartRequest) (*task.StartResponse, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	e, err := t.GetExec(req.ExecID)
	if err != nil {
		return nil, err
	}
	err = e.Start(ctx)
	if err != nil {
		return nil, err
	}
	return &task.StartResponse{
		Pid: uint32(e.Pid()),
	}, nil
}

func (s *Service) deleteInternal(ctx context.Context, req *task.DeleteRequest) (*task.DeleteResponse, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}

	pid, exitStatus, exitedAt, err := t.DeleteExec(ctx, req.ExecID)
	if err != nil {
		return nil, err
	}

	// if the delete is for a task and not an exec, remove the pod sandbox's reference to the task
	if s.isSandbox && req.ExecID == "" {
		p, err := s.getPod()
		if err != nil {
			return nil, errors.Wrapf(err, "could not get pod %q to delete task %q", s.tid, req.ID)
		}
		err = p.DeleteTask(ctx, req.ID)
		if err != nil {
			return nil, fmt.Errorf("could not delete task %q in pod %q: %w", req.ID, s.tid, err)
		}
	}
	// TODO: check if the pod's workload tasks is empty, and, if so, reset p.taskOrPod to nil

	return &task.DeleteResponse{
		Pid:        uint32(pid),
		ExitStatus: exitStatus,
		ExitedAt:   exitedAt,
	}, nil
}

func (s *Service) pidsInternal(ctx context.Context, req *task.PidsRequest) (*task.PidsResponse, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	pids, err := t.Pids(ctx)
	if err != nil {
		return nil, err
	}
	processes := make([]*containerd_v1_types.ProcessInfo, len(pids))
	for i, p := range pids {
		a, err := typeurl.MarshalAny(&p)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to marshal ProcessDetails for process: %s, task: %s", p.ExecID, req.ID)
		}
		proc := &containerd_v1_types.ProcessInfo{
			Pid:  p.ProcessID,
			Info: a,
		}
		processes[i] = proc
	}
	return &task.PidsResponse{
		Processes: processes,
	}, nil
}

func (s *Service) pauseInternal(ctx context.Context, req *task.PauseRequest) (*google_protobuf1.Empty, error) {
	/*
		s.events <- cdevent{
			topic: runtime.TaskPausedEventTopic,
			event: &eventstypes.TaskPaused{
				req.ID,
			},
		}
	*/
	return nil, errdefs.ErrNotImplemented
}

func (s *Service) resumeInternal(ctx context.Context, req *task.ResumeRequest) (*google_protobuf1.Empty, error) {
	/*
		s.events <- cdevent{
			topic: runtime.TaskResumedEventTopic,
			event: &eventstypes.TaskResumed{
				req.ID,
			},
		}
	*/
	return nil, errdefs.ErrNotImplemented
}

func (s *Service) checkpointInternal(ctx context.Context, req *task.CheckpointTaskRequest) (*google_protobuf1.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *Service) killInternal(ctx context.Context, req *task.KillRequest) (*google_protobuf1.Empty, error) {
	if s.isSandbox {
		pod, err := s.getPod()
		if err != nil {
			return nil, errors.Wrapf(errdefs.ErrNotFound, "%v: task with id: '%s' not found", err, req.ID)
		}
		// Send it to the POD and let it cascade on its own through all tasks.
		err = pod.KillTask(ctx, req.ID, req.ExecID, req.Signal, req.All)
		if err != nil {
			return nil, err
		}
		return empty, nil
	}
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	// Send it to the task and let it cascade on its own through all exec's
	err = t.KillExec(ctx, req.ExecID, req.Signal, req.All)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) execInternal(ctx context.Context, req *task.ExecProcessRequest) (*google_protobuf1.Empty, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	if req.Terminal && req.Stderr != "" {
		return nil, errors.Wrap(errdefs.ErrFailedPrecondition, "if using terminal, stderr must be empty")
	}
	var spec specs.Process
	if err := json.Unmarshal(req.Spec.Value, &spec); err != nil {
		return nil, errors.Wrap(err, "request.Spec was not oci process")
	}
	err = t.CreateExec(ctx, req, &spec)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) diagExecInHostInternal(ctx context.Context, req *shimdiag.ExecProcessRequest) (*shimdiag.ExecProcessResponse, error) {
	if req.Terminal && req.Stderr != "" {
		return nil, errors.Wrap(errdefs.ErrFailedPrecondition, "if using terminal, stderr must be empty")
	}
	t, err := s.getTask(s.tid)
	if err != nil {
		return nil, err
	}
	ec, err := t.ExecInHost(ctx, req)
	if err != nil {
		return nil, err
	}
	return &shimdiag.ExecProcessResponse{ExitCode: int32(ec)}, nil
}

func (s *Service) diagShareInternal(ctx context.Context, req *shimdiag.ShareRequest) (*shimdiag.ShareResponse, error) {
	t, err := s.getTask(s.tid)
	if err != nil {
		return nil, err
	}
	if err := t.Share(ctx, req); err != nil {
		return nil, err
	}
	return &shimdiag.ShareResponse{}, nil
}

func (s *Service) resizePtyInternal(ctx context.Context, req *task.ResizePtyRequest) (*google_protobuf1.Empty, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	e, err := t.GetExec(req.ExecID)
	if err != nil {
		return nil, err
	}
	err = e.ResizePty(ctx, req.Width, req.Height)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) closeIOInternal(ctx context.Context, req *task.CloseIORequest) (*google_protobuf1.Empty, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	e, err := t.GetExec(req.ExecID)
	if err != nil {
		return nil, err
	}
	err = e.CloseIO(ctx, req.Stdin)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) updateInternal(ctx context.Context, req *task.UpdateTaskRequest) (*google_protobuf1.Empty, error) {
	if req.Resources == nil {
		return nil, errors.Wrapf(errdefs.ErrInvalidArgument, "resources cannot be empty, updating container %s resources failed", req.ID)
	}
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	if err := t.Update(ctx, req); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) waitInternal(ctx context.Context, req *task.WaitRequest) (*task.WaitResponse, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	var state *task.StateResponse
	if req.ExecID != "" {
		e, err := t.GetExec(req.ExecID)
		if err != nil {
			return nil, err
		}
		state = e.Wait()
	} else {
		state = t.Wait()
	}
	return &task.WaitResponse{
		ExitStatus: state.ExitStatus,
		ExitedAt:   state.ExitedAt,
	}, nil
}

func (s *Service) statsInternal(ctx context.Context, req *task.StatsRequest) (*task.StatsResponse, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	stats, err := t.Stats(ctx)
	if err != nil {
		return nil, err
	}
	any, err := typeurl.MarshalAny(stats)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal Statistics for task: %s", req.ID)
	}
	return &task.StatsResponse{Stats: any}, nil
}

func (s *Service) connectInternal(ctx context.Context, req *task.ConnectRequest) (*task.ConnectResponse, error) {
	// We treat the shim/task as the same pid on the Windows host.
	pid := uint32(os.Getpid())
	return &task.ConnectResponse{
		ShimPid: pid,
		TaskPid: pid,
	}, nil
}

func (s *Service) shutdownInternal(ctx context.Context, req *task.ShutdownRequest) (*google_protobuf1.Empty, error) {
	// Because a pod shim hosts multiple tasks only the init task can issue the
	// shutdown request.
	if req.ID != s.tid {
		return empty, nil
	}

	s.shutdownOnce.Do(func() {
		// TODO: should taskOrPod be deleted/set to nil?
		// TODO: is there any extra leftovers of the shimTask/Pod to clean? ie: verify all handles are closed?
		s.gracefulShutdown = !req.Now
		close(s.shutdown)
	})

	return empty, nil
}

func (s *Service) computeProcessorInfoInternal(ctx context.Context, req *extendedtask.ComputeProcessorInfoRequest) (*extendedtask.ComputeProcessorInfoResponse, error) {
	t, err := s.getTask(req.ID)
	if err != nil {
		return nil, err
	}
	info, err := t.ProcessorInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &extendedtask.ComputeProcessorInfoResponse{
		Count: info.Count,
	}, nil
}
