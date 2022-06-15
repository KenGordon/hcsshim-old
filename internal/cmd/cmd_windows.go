package cmd

import (
	"strings"

	"github.com/Microsoft/hcsshim/internal/cow"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

func escapeArgs(args []string) string {
	escapedArgs := make([]string, len(args))
	for i, a := range args {
		escapedArgs[i] = windows.EscapeArg(a)
	}
	return strings.Join(escapedArgs, " ")
}

// Command makes a Cmd for a given command and arguments.
func Command(host cow.ProcessHost, name string, arg ...string) *Cmd {
	cmd := &Cmd{
		Host: host,
		Spec: &specs.Process{
			Args: append([]string{name}, arg...),
		},
		Log:       logrus.NewEntry(logrus.StandardLogger()),
		ExitState: &ExitState{},
	}
	if host.OS() == "windows" {
		cmd.Spec.Cwd = `C:\`
	} else {
		cmd.Spec.Cwd = "/"
		cmd.Spec.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	}
	return cmd
}

func (c *Cmd) createCommandConfig() interface{} {
	var x interface{}
	if !c.Host.IsOCI() {
		wpp := &hcsschema.ProcessParameters{
			CommandLine:      c.Spec.CommandLine,
			User:             c.Spec.User.Username,
			WorkingDirectory: c.Spec.Cwd,
			EmulateConsole:   c.Spec.Terminal,
			CreateStdInPipe:  c.Stdin != nil,
			CreateStdOutPipe: c.Stdout != nil,
			CreateStdErrPipe: c.Stderr != nil,
		}

		if c.Spec.CommandLine == "" {
			if c.Host.OS() == "windows" {
				wpp.CommandLine = escapeArgs(c.Spec.Args)
			} else {
				wpp.CommandArgs = c.Spec.Args
			}
		}

		environment := make(map[string]string)
		for _, v := range c.Spec.Env {
			s := strings.SplitN(v, "=", 2)
			if len(s) == 2 && len(s[1]) > 0 {
				environment[s[0]] = s[1]
			}
		}
		wpp.Environment = environment

		if c.Spec.ConsoleSize != nil {
			wpp.ConsoleSize = []int32{
				int32(c.Spec.ConsoleSize.Height),
				int32(c.Spec.ConsoleSize.Width),
			}
		}
		x = wpp
	} else {
		lpp := &lcowProcessParameters{
			ProcessParameters: hcsschema.ProcessParameters{
				CreateStdInPipe:  c.Stdin != nil,
				CreateStdOutPipe: c.Stdout != nil,
				CreateStdErrPipe: c.Stderr != nil,
			},
			OCIProcess: c.Spec,
		}
		x = lpp
	}
	return x
}
