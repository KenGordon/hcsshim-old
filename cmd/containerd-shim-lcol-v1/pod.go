//go:build !windows
// +build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/Microsoft/hcsshim/internal/hvlitevm"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/internal/shim"
	shimservice "github.com/Microsoft/hcsshim/internal/shim"
	"github.com/Microsoft/hcsshim/pkg/annotations"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type lcolPodFactory struct{}

var _ = (shim.PodFactory)(&lcolPodFactory{})

func (h *lcolPodFactory) Create(ctx context.Context, events events.Publisher, req *task.CreateTaskRequest, s *specs.Spec) (shim.Pod, error) {
	log.G(ctx).WithField("tid", req.ID).Debug("create lcol pod")

	ct, sid, err := oci.GetSandboxTypeAndID(s.Annotations)
	if err != nil {
		return nil, err
	}
	if ct != oci.KubernetesContainerTypeSandbox {
		return nil, errors.Wrapf(
			errdefs.ErrFailedPrecondition,
			"expected annotation: '%s': '%s' got '%s'",
			annotations.KubernetesContainerType,
			oci.KubernetesContainerTypeSandbox,
			ct)
	}
	if sid != req.ID {
		return nil, errors.Wrapf(
			errdefs.ErrFailedPrecondition,
			"expected annotation '%s': '%s' got '%s'",
			annotations.KubernetesSandboxID,
			req.ID,
			sid)
	}

	owner := filepath.Base(os.Args[0])

	p := lcolPod{
		events: events,
		id:     req.ID,
	}

	opts := hvlitevm.NewDefaultOptions(req.ID, owner)
	opts.OCISpec = s

	parent, err := hvlitevm.Create(ctx, opts)
	if err != nil {
		return nil, err
	}

	err = parent.Start(ctx)
	if err != nil {
		parent.Close()
		return nil, err
	}

	defer func() {
		// clean up the uvm if we fail any further operations
		if err != nil && parent != nil {
			parent.Close()
		}
	}()

	p.host = parent

	if err := parent.AddEndpointsToNS(ctx); err != nil {
		return nil, err
	}

	if s.Windows == nil {
		s.Windows = &specs.Windows{}
	}
	if s.Windows.Network == nil {
		s.Windows.Network = &specs.WindowsNetwork{}
	}
	specNamespaces := s.Linux.Namespaces
	nses := []specs.LinuxNamespace{}
	for _, namespace := range specNamespaces {
		if namespace.Type == specs.NetworkNamespace {
			// remove linux namespace path to play nice with gcs + runc
			s.Windows.Network.NetworkNamespace = namespace.Path
			namespace.Path = ""
		}
		nses = append(nses, namespace)
	}
	s.Linux.Namespaces = nses

	log.G(ctx).WithField("specNamespace", specNamespaces).Info("spec namesapce used ")

	lt, err := newLCOLTask(ctx, events, parent, true, req, s)
	if err != nil {
		return nil, err
	}
	p.sandboxTask = lt
	return &p, nil
}

type lcolPod struct {
	events events.Publisher
	// id is the id of the sandbox task when the pod is created.
	//
	// It MUST be treated as read only in the lifetime of the pod.
	id string
	// sandboxTask is the task that represents the sandbox.
	//
	// Note: The invariant `id==sandboxTask.ID()` MUST be true.
	//
	// It MUST be treated as read only in the lifetime of the pod.
	sandboxTask shimservice.Task
	// host is the UtilityVM that is hosting `sandboxTask` if the task is
	// hypervisor isolated.
	//
	// It MUST be treated as read only in the lifetime of the pod.
	host *hvlitevm.UtilityVM

	workloadTasks sync.Map
}

var _ = (shim.Pod)(&lcolPod{})

// ID is the id of the task representing the pause (sandbox) container.
func (p *lcolPod) ID() string {
	return p.id
}

// CreateTask creates a workload task within this pod named `tid` with
// settings `s`.
//
// If `tid==ID()` or `tid` is the same as any other task in this pod, this
// pod MUST return `errdefs.ErrAlreadyExists`.
func (p *lcolPod) CreateTask(ctx context.Context, req *task.CreateTaskRequest, s *specs.Spec) (shimservice.Task, error) {
	if req.ID == p.id {
		return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "task with id: '%s' already exists", req.ID)
	}
	e, _ := p.sandboxTask.GetExec("")
	if e.State() != shimservice.ExecStateRunning {
		return nil, errors.Wrapf(errdefs.ErrFailedPrecondition, "task with id: '%s' cannot be created in pod: '%s' which is not running", req.ID, p.id)
	}

	_, ok := p.workloadTasks.Load(req.ID)
	if ok {
		return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "task with id: '%s' already exists id pod: '%s'", req.ID, p.id)
	}

	ct, sid, err := oci.GetSandboxTypeAndID(s.Annotations)
	if err != nil {
		return nil, err
	}
	if ct != oci.KubernetesContainerTypeContainer {
		return nil, errors.Wrapf(
			errdefs.ErrFailedPrecondition,
			"expected annotation: '%s': '%s' got '%s'",
			annotations.KubernetesContainerType,
			oci.KubernetesContainerTypeContainer,
			ct)
	}
	if sid != p.id {
		return nil, errors.Wrapf(
			errdefs.ErrFailedPrecondition,
			"expected annotation '%s': '%s' got '%s'",
			annotations.KubernetesSandboxID,
			p.id,
			sid)
	}

	st, err := newLCOLTask(ctx, p.events, p.host, false, req, s)
	if err != nil {
		return nil, err
	}

	p.workloadTasks.Store(req.ID, st)
	return st, nil
}

// GetTask returns a task in this pod that matches `tid`.
//
// If `tid` is not found, this pod MUST return `errdefs.ErrNotFound`.
func (p *lcolPod) GetTask(tid string) (shimservice.Task, error) {
	// load task
	if tid == p.id {
		return p.sandboxTask, nil
	}
	raw, loaded := p.workloadTasks.Load(tid)
	if !loaded {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "task with id: '%s' not found", tid)
	}
	return raw.(shimservice.Task), nil
}

// KillTask sends `signal` to task that matches `tid`.
//
// If `tid` is not found, this pod MUST return `errdefs.ErrNotFound`.
//
// If `tid==ID() && eid == "" && all == true` this pod will send `signal` to
// all tasks in the pod and lastly send `signal` to the sandbox itself.
//
// If `all == true && eid != ""` this pod MUST return
// `errdefs.ErrFailedPrecondition`.
//
// A call to `KillTask` is only valid when the exec found by `tid,eid` is in
// the `shimExecStateRunning, shimExecStateExited` states. If the exec is
// not in this state this pod MUST return `errdefs.ErrFailedPrecondition`.
func (p *lcolPod) KillTask(ctx context.Context, tid, eid string, signal uint32, all bool) error {
	t, err := p.GetTask(tid)
	if err != nil {
		return err
	}
	if all && eid != "" {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "cannot signal all with non empty ExecID: '%s'", eid)
	}
	eg := errgroup.Group{}
	if all && tid == p.id {
		// We are in a kill all on the sandbox task. Signal everything.
		p.workloadTasks.Range(func(key, value interface{}) bool {
			wt := value.(shimservice.Task)
			eg.Go(func() error {
				return wt.KillExec(ctx, eid, signal, all)
			})

			// Iterate all. Returning false stops the iteration. See:
			// https://pkg.go.dev/sync#Map.Range
			return true
		})
	}
	eg.Go(func() error {
		return t.KillExec(ctx, eid, signal, all)
	})
	return eg.Wait()
}

// DeleteTask removes a task from being tracked by this pod, and cleans up
// the resources the shim allocated for the task.
//
// The task's init exec (eid == "") must be either `shimExecStateCreated` or
// `shimExecStateExited`.  If the exec is not in this state this pod MUST
// return `errdefs.ErrFailedPrecondition`. Deleting the pod's sandbox task
// is a no-op.
func (p *lcolPod) DeleteTask(ctx context.Context, tid string) error {
	// Deleting the sandbox task is a no-op, since the service should delete its
	// reference to the sandbox task or pod, and `p.sandboxTask != nil` is an
	// invariant that is relied on elsewhere.
	// However, still get the init exec for all tasks to ensure that they have
	// been properly stopped.

	t, err := p.GetTask(tid)
	if err != nil {
		return errors.Wrap(err, "could not find task to delete")
	}

	e, err := t.GetExec("")
	if err != nil {
		return errors.Wrap(err, "could not get initial exec")
	}
	if e.State() == shimservice.ExecStateRunning {
		return errors.Wrap(errdefs.ErrFailedPrecondition, "cannot delete task with running exec")
	}

	if p.id != tid {
		p.workloadTasks.Delete(tid)
	}

	return nil
}
