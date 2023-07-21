package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/Microsoft/hcsshim/internal/hns"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/osversion"
	"github.com/Microsoft/hcsshim/pkg/annotations"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/runtime"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sync/errgroup"
)

type vmPod struct {
	events        publisher
	id            string
	sandboxTask   shimTask
	workloadTasks sync.Map
	network       *hns.HNSNetwork
}

var _ = (shimPod)(&vmPod{})

func (vp *vmPod) ID() string {
	return vp.id
}

func (vp *vmPod) CreateTask(ctx context.Context, req *task.CreateTaskRequest, s *specs.Spec) (shimTask, error) {
	if req.ID == vp.id {
		return nil, fmt.Errorf("task with id '%s' already exists: %w", req.ID, errdefs.ErrAlreadyExists)
	}
	e, _ := vp.sandboxTask.GetExec("")
	if e.State() != shimExecStateRunning {
		return nil, fmt.Errorf("task with id '%s' cannot be created in pod '%s' which is not running: %w", req.ID,
			vp.id, errdefs.ErrFailedPrecondition)
	}

	_, ok := vp.workloadTasks.Load(req.ID)
	if ok {
		return nil, fmt.Errorf("task with id '%s' already exists in pod '%s': %w", req.ID, vp.id, errdefs.ErrAlreadyExists)
	}

	ct, sid, err := oci.GetSandboxTypeAndID(s.Annotations)
	if err != nil {
		return nil, err
	}

	if ct != oci.KubernetesContainerTypeContainer {
		return nil, fmt.Errorf("expected annotation '%s': '%s' got '%s': %w",
			annotations.KubernetesContainerType,
			oci.KubernetesContainerTypeContainer,
			ct,
			errdefs.ErrFailedPrecondition,
		)
	}
	if sid != vp.id {
		return nil, fmt.Errorf("expected annotation '%s': '%s' got '%s': %w",
			annotations.KubernetesSandboxID,
			vp.id,
			sid,
			errdefs.ErrFailedPrecondition,
		)
	}

	st, err := newHcsVMTask(ctx, vp.events, req, s)
	if err != nil {
		return nil, err
	}
	vp.workloadTasks.Store(req.ID, st)
	return st, nil
}

func (vp *vmPod) GetTask(tid string) (shimTask, error) {
	if tid == vp.id {
		return vp.sandboxTask, nil
	}
	raw, ok := vp.workloadTasks.Load(tid)
	if !ok {
		return nil, fmt.Errorf("task with id %q not found: %w", tid, errdefs.ErrNotFound)
	}
	return raw.(shimTask), nil
}

func (vp *vmPod) ListTasks() (_ []shimTask, err error) {
	tasks := []shimTask{vp.sandboxTask}
	vp.workloadTasks.Range(func(key, value interface{}) bool {
		wt, loaded := value.(shimTask)
		if !loaded {
			err = fmt.Errorf("failed to load tasks %s", key)
			return false
		}
		tasks = append(tasks, wt)
		return true
	})
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

func (vp *vmPod) KillTask(ctx context.Context, tid, eid string, signal uint32, all bool) error {
	t, err := vp.GetTask(tid)
	if err != nil {
		return err
	}
	eg := errgroup.Group{}
	if all && tid == vp.id {
		vp.workloadTasks.Range(func(key, value interface{}) bool {
			wt := value.(shimTask)
			eg.Go(func() error {
				return wt.KillExec(ctx, eid, signal, all)
			})
			return true
		})
	}
	eg.Go(func() error {
		return t.KillExec(ctx, eid, signal, all)
	})
	return eg.Wait()
}

func (vp *vmPod) DeleteTask(ctx context.Context, tid string) error {
	t, err := vp.GetTask(tid)
	if err != nil {
		return fmt.Errorf("caould not find task to delete: %w", err)
	}

	e, err := t.GetExec("")
	if err != nil {
		return fmt.Errorf("could not get initial exec: %w", err)
	}
	if e.State() == shimExecStateRunning {
		return fmt.Errorf("cannot delete task with running exec: %w", errdefs.ErrFailedPrecondition)
	}

	if vp.id != tid {
		vp.workloadTasks.Delete(tid)
	}
	return nil
}

func createVMPod(
	ctx context.Context,
	events publisher,
	req *task.CreateTaskRequest,
) (_ shimPod, err error) {
	log.G(ctx).WithField("tid", req.ID).Debug("createVMPod")

	if osversion.Build() < osversion.RS5 {
		return nil, fmt.Errorf("pod support is not available on Windows versions pervious to RS5: %w", errdefs.ErrFailedPrecondition)
	}

	p := &vmPod{
		events: events,
		id:     req.ID,
	}
	p.sandboxTask = newWcowPodSandboxTask(ctx, events, req.ID, req.Bundle, nil, "")

	if evntErr := events.publishEvent(
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
			Pid:        0,
		},
	); evntErr != nil {
		log.G(ctx).WithError(evntErr).Debug("error publishing task create event")
	}
	return p, nil
}
