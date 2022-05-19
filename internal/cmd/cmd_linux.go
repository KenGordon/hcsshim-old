package cmd

import (
	"github.com/Microsoft/hcsshim/internal/cow"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

// Command makes a Cmd for a given command and arguments.
func Command(host cow.ProcessHost, name string, arg ...string) *Cmd {
	cmd := &Cmd{
		Host: host,
		Spec: &specs.Process{
			Args: append([]string{name}, arg...),
			Cwd:  "/",
			Env:  []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		},
		Log:       logrus.NewEntry(logrus.StandardLogger()),
		ExitState: &ExitState{},
	}
	return cmd
}

func (c *Cmd) createCommandConfig() interface{} {
	lpp := &lcowProcessParameters{
		ProcessParameters: hcsschema.ProcessParameters{
			CreateStdInPipe:  c.Stdin != nil,
			CreateStdOutPipe: c.Stdout != nil,
			CreateStdErrPipe: c.Stderr != nil,
		},
		OCIProcess: c.Spec,
	}
	return lpp
}
