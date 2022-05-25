package remotevm

import "os/exec"

func (uvmb *utilityVMBuilder) startVMM(cmd *exec.Cmd) error {
	return cmd.Start()
}
