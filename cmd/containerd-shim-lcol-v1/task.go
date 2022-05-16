//go:build !windows
// +build !windows

package main

import (
	"context"
	"time"

	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/service/options"
	"github.com/Microsoft/hcsshim/internal/service/stats"
	"github.com/Microsoft/hcsshim/internal/shim"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/opencontainers/runtime-spec/specs-go"
)

type lcolTask struct {
	events    events.Publisher
	id        string
	container cow.Container
	// TODO katiewasnothere: this probably needs to be fixed to
	// build on linux
	containerResources *resources.Resources
	host               *vm.UVM
}

var _ = (shim.Task)(&lcolTask{})

// ID returns the id of the task
func (t *lcolTask) ID() string {
	return t.id
}

// CreateExec creates a new exec within this task
func (t *lcolTask) CreateExec(ctx context.Context, req *task.ExecProcessRequest, s *specs.Process) error {
	return nil
}

// GetExec returns an exec in this task with the id `eid`
func (t *lcolTask) GetExec(eid string) (shim.Exec, error) {
	return nil, nil
}

// KillExec sends `signal` to exec with id `eid`
func (t *lcolTask) KillExec(ctx context.Context, eid string, signal uint32, all bool) error {
	return nil
}

// DeleteExec deletes an exec with id `eid` in this task
func (t *lcolTask) DeleteExec(ctx context.Context, eid string) (int, uint32, time.Time, error) {

}

// Pids returns all process pids in this task
func (t *lcolTask) Pids(ctx context.Context) ([]options.ProcessDetails, error) {

}

// Wait for init task to complete
func (t *lcolTask) Wait() *task.StateResponse {

}

// ExecInHost executes a process in the host of this task
func (t *lcolTask) ExecInHost(ctx context.Context, req *shimdiag.ExecProcessRequest) (int, error) {

}

// DumpGuestStacks dumps the GCS stacks associated with this task
func (t *lcolTask) DumpGuestStacks(ctx context.Context) string {

}

// Share shares a directory/file into the task host
func (t *lcolTask) Share(ctx context.Context, req *shimdiag.ShareRequest) error {

}

// Stats returns metrics for the task
func (t *lcolTask) Stats(ctx context.Context) (*stats.Statistics, error) {

}

// ProcessInfo returns information on the task's processor settings
func (t *lcolTask) ProcessorInfo(ctx context.Context) (*shim.ProcessorInfo, error) {

}

// Update updates a task's container settings/resources
func (t *lcolTask) Update(ctx context.Context, req *task.UpdateTaskRequest) error {

}
