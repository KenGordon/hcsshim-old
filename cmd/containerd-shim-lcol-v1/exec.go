//go:build !windows
// +build !windows

package main

import (
	"context"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/shim"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/opencontainers/runtime-spec/specs-go"
)

type lcolExecState struct {
	m          sync.Mutex
	state      shim.ExecState
	pid        int
	exitStatus uint32
	exitedAt   time.Time
	processCmd *cmd.Cmd
}

type lcolExec struct {
	events    events.Publisher
	id        string
	tid       string
	host      *uvm.UtilityVM // katiewasnothere
	container cow.Container
	bundle    string
	spec      *specs.Process
	// TODO katiewasnothere: do we need this upstreamIO thing here?
	io    cmd.UpstreamIO
	state *lcolExecState
}

func (e *lcolExec) ID() string {

}

func (e *lcolExec) Pid() int {

}

func (e *lcolExec) State() shim.ExecState {

}

func (e *lcolExec) Status() *task.StateResponse {

}

func (e *lcolExec) Start(ctx context.Context) error {

}

// Kill sends `signal` to this exec process
func (e *lcolExec) Kill(ctx context.Context, signal uint32) error {

}

func (e *lcolExec) ResizePty(ctx context.Context, width, height uint32) error {

}

func (e *lcolExec) CloseIO(ctx context.Context, stdin bool) error {

}

func (e *lcolExec) Wait() *task.StateResponse {

}

func (e *lcolExec) ForceExit(ctx context.Context, status int) {

}
