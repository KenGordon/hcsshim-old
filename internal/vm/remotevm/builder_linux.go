package remotevm

import (
	"github.com/Microsoft/hcsshim/internal/guest/network"
	"github.com/vishvananda/netns"
	"os/exec"
)

func (uvmb *utilityVMBuilder) startVMM(cmd *exec.Cmd) error {
	startProc := func() error {
		return cmd.Start()
	}
	if uvmb.networkNS != "" {
		nsHandle, err := netns.GetFromName(uvmb.networkNS)
		if err != nil {
			return err
		}
		defer nsHandle.Close()
		return network.DoInNetNS(nsHandle, startProc)
	}
	return startProc()
}
