package remotevm

import (
	"fmt"
	"os"

	"github.com/Microsoft/hcsshim/internal/vmservice"
)

func (uvmb *utilityVMBuilder) SetVirtiofsMount(tag, root string) error {
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("failed to stat virtiofs host path: %w", err)
	}
	c := &vmservice.VirtioFSConfig{Tag: tag, RootPath: root}
	uvmb.config.DevicesConfig.VirtiofsConfig = append(uvmb.config.DevicesConfig.VirtiofsConfig, c)
	return nil
}
