package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	cmdio "github.com/Microsoft/hcsshim/internal/cmd/io"
	"github.com/Microsoft/hcsshim/internal/cow"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// CmdProcessRequest stores information on command requests made through this package.
type CmdProcessRequest struct {
	Args     []string
	Workdir  string
	Terminal bool
	Stdin    string
	Stdout   string
	Stderr   string
}

// Cmd represents a command being prepared or run in a process host.
type Cmd struct {
	// Host is the process host in which to launch the process.
	Host cow.ProcessHost

	// The OCI spec for the process.
	Spec *specs.Process

	// Standard IO streams to relay to/from the process.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Log provides a logrus entry to use in logging IO copying status.
	Log *logrus.Entry

	// Context provides a context that terminates the process when it is done.
	Context context.Context

	// CopyAfterExitTimeout is the amount of time after process exit we allow the
	// stdout, stderr relays to continue before forcibly closing them if not
	// already completed. This is primarily a safety step against the HCS when
	// it fails to send a close on the stdout, stderr pipes when the process
	// exits and blocks the relay wait groups forever.
	CopyAfterExitTimeout time.Duration

	// Process is filled out after Start() returns.
	Process cow.Process

	// ExitState is filled out after Wait() (or Run() or Output()) completes.
	ExitState *ExitState

	iogrp     errgroup.Group
	stdinErr  atomic.Value
	allDoneCh chan struct{}
}

// ExitState contains whether a process has exited and with which exit code.
type ExitState struct {
	exited bool
	code   int
}

// ExitCode returns the exit code of the process, or -1 if the exit code is not known.
func (s *ExitState) ExitCode() int {
	if !s.exited {
		return -1
	}
	return s.code
}

// ExitError is used when a process exits with a non-zero exit code.
type ExitError struct {
	*ExitState
}

func (err *ExitError) Error() string {
	return fmt.Sprintf("process exited with exit code %d", err.ExitCode())
}

// Additional fields to hcsschema.ProcessParameters used by LCOW
type lcowProcessParameters struct {
	hcsschema.ProcessParameters
	OCIProcess *specs.Process `json:"OciProcess,omitempty"`
}

// CommandContext makes a Cmd for a given command and arguments. After
// it is launched, the process is killed when the context becomes done.
func CommandContext(ctx context.Context, host cow.ProcessHost, name string, arg ...string) *Cmd {
	cmd := Command(host, name, arg...)
	cmd.Context = ctx
	cmd.Log = log.G(ctx)
	return cmd
}

// Start starts a command. The caller must ensure that if Start succeeds,
// Wait is eventually called to clean up resources.
func (c *Cmd) Start() error {
	c.allDoneCh = make(chan struct{})

	config := c.createCommandConfig()
	if c.Context != nil && c.Context.Err() != nil {
		return c.Context.Err()
	}
	p, err := c.Host.CreateProcess(context.TODO(), config)
	if err != nil {
		return err
	}
	c.Process = p
	if c.Log != nil {
		c.Log = c.Log.WithField("pid", p.Pid())
	}

	// Start relaying process IO.
	stdin, stdout, stderr := p.Stdio()
	if c.Stdin != nil {
		// Do not make stdin part of the error group because there is no way for
		// us or the caller to reliably unblock the c.Stdin read when the
		// process exits.
		go func() {
			_, err := cmdio.RelayIO(stdin, c.Stdin, c.Log, "stdin")
			// Report the stdin copy error. If the process has exited, then the
			// caller may never see it, but if the error was due to a failure in
			// stdin read, then it is likely the process is still running.
			if err != nil {
				c.stdinErr.Store(err)
			}
			// Notify the process that there is no more input.
			if err := p.CloseStdin(context.TODO()); err != nil && c.Log != nil {
				c.Log.WithError(err).Warn("failed to close Cmd stdin")
			}
		}()
	}

	if c.Stdout != nil {
		c.iogrp.Go(func() error {
			_, err := cmdio.RelayIO(c.Stdout, stdout, c.Log, "stdout")
			if err := p.CloseStdout(context.TODO()); err != nil {
				c.Log.WithError(err).Warn("failed to close Cmd stdout")
			}
			return err
		})
	}

	if c.Stderr != nil {
		c.iogrp.Go(func() error {
			_, err := cmdio.RelayIO(c.Stderr, stderr, c.Log, "stderr")
			if err := p.CloseStderr(context.TODO()); err != nil {
				c.Log.WithError(err).Warn("failed to close Cmd stderr")
			}
			return err
		})
	}

	if c.Context != nil {
		go func() {
			select {
			case <-c.Context.Done():
				// Process.Kill (via Process.Signal) will not send an RPC if the
				// provided context in is cancelled (bridge.AsyncRPC will end early)
				ctx := c.Context
				if ctx == nil {
					ctx = context.Background()
				}
				kctx := log.Copy(context.Background(), ctx)
				_, _ = c.Process.Kill(kctx)
			case <-c.allDoneCh:
			}
		}()
	}
	return nil
}

// Wait waits for a command and its IO to complete and closes the underlying
// process. It can only be called once. It returns an ExitError if the command
// runs and returns a non-zero exit code.
func (c *Cmd) Wait() error {
	waitErr := c.Process.Wait()
	if waitErr != nil && c.Log != nil {
		c.Log.WithError(waitErr).Warn("process wait failed")
	}
	state := &ExitState{}
	code, exitErr := c.Process.ExitCode()
	if exitErr == nil {
		state.exited = true
		state.code = code
	}
	// Terminate the IO if the copy does not complete in the requested time.
	if c.CopyAfterExitTimeout != 0 {
		go func() {
			t := time.NewTimer(c.CopyAfterExitTimeout)
			defer t.Stop()
			select {
			case <-c.allDoneCh:
			case <-t.C:
				// Close the process to cancel any reads to stdout or stderr.
				c.Process.Close()
				if c.Log != nil {
					c.Log.Warn("timed out waiting for stdio relay")
				}
			}
		}()
	}
	ioErr := c.iogrp.Wait()
	if ioErr == nil {
		ioErr, _ = c.stdinErr.Load().(error)
	}
	close(c.allDoneCh)
	c.Process.Close()
	c.ExitState = state
	if exitErr != nil {
		return exitErr
	}
	if state.exited && state.code != 0 {
		return &ExitError{state}
	}
	return ioErr
}

// Run is equivalent to Start followed by Wait.
func (c *Cmd) Run() error {
	err := c.Start()
	if err != nil {
		return err
	}
	return c.Wait()
}

// Output runs a command via Run and collects its stdout into a buffer,
// which it returns.
func (c *Cmd) Output() ([]byte, error) {
	var b bytes.Buffer
	c.Stdout = &b
	err := c.Run()
	return b.Bytes(), err
}
