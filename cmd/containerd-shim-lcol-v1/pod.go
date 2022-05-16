//go:build !windows
// +build !windows

package main

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/shim"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/opencontainers/runtime-spec/specs-go"
)

type lcolPod struct {
}

var _ = (shim.Pod)(&lcolPod{})

// ID is the id of the task representing the pause (sandbox) container.
func (p *lcolPod) ID() string {

}

// CreateTask creates a workload task within this pod named `tid` with
// settings `s`.
//
// If `tid==ID()` or `tid` is the same as any other task in this pod, this
// pod MUST return `errdefs.ErrAlreadyExists`.
func (p *lcolPod) CreateTask(ctx context.Context, req *task.CreateTaskRequest, s *specs.Spec) (Task, error) {
	// get the sandbox exec to make sure exec is in running state

	// check if the task already exists

	// create new task

	// store task
}

// GetTask returns a task in this pod that matches `tid`.
//
// If `tid` is not found, this pod MUST return `errdefs.ErrNotFound`.
func (p *lcolPod) GetTask(tid string) (Task, error) {
	// load task
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
	// get the task from the pod

	// if all is set and this is the owner pod id
	// go through all tasks and issue KillExec

	// otherwise just issue to the given eid
}

// DeleteTask removes a task from being tracked by this pod, and cleans up
// the resources the shim allocated for the task.
//
// The task's init exec (eid == "") must be either `shimExecStateCreated` or
// `shimExecStateExited`.  If the exec is not in this state this pod MUST
// return `errdefs.ErrFailedPrecondition`. Deleting the pod's sandbox task
// is a no-op.
func (p *lcolPod) DeleteTask(ctx context.Context, tid string) error {
	// get task

	// get exec of task

	// check state of exec

	// delete from list of tasks
}
