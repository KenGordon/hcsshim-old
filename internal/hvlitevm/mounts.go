package hvlitevm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/Microsoft/hcsshim/internal/vm"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

const (
	SharedHostPathFmt     = "/run/hvl/shared/vm/%s"
	SharedHostPathCntrFmt = "/run/hvl/shared/vm/%s/cntr/%s"
)

func setupVirtiofs(ctx context.Context, id string, builder vm.UVMBuilder) (string, error) {
	log.G(ctx).Infof("Setting up shared FS for VM %s", id)

	sharePath := fmt.Sprintf(SharedHostPathFmt, id)
	if err := os.MkdirAll(sharePath, 0700); err != nil {
		return "", fmt.Errorf("failed to create virtiofs host dir: %w", err)
	}

	vfs, ok := builder.(vm.VirtiofsManager)
	if !ok {
		return "", fmt.Errorf("failed to add virtiofs mount: %w", vm.ErrNotSupported)
	}

	if err := vfs.SetVirtiofsMount(id, sharePath); err != nil {
		return "", err
	}

	return sharePath, nil
}

func (uvm *UtilityVM) setupMounts(ctx context.Context, cntrID string, s *specs.Spec) error {
	log.G(ctx).Infof("Setting up mounts for %s", cntrID)

	// Need to make a guest req to actually mount the virtiofs mount if we haven't already. No support over ttrpc for
	// hot add so creation and the guestreq to mount happen at different stages, just need to be mindful of this.
	if len(s.Mounts) != 0 && atomic.CompareAndSwapUint32(uvm.sharedFSMounted, 0, 1) {
		guestReq := guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeVirtiofs,
			RequestType:  guestrequest.RequestTypeAdd,
		}

		guestReq.Settings = guestresource.VirtiofsMappedDir{
			Source:    uvm.ID,
			MountPath: fmt.Sprintf(SharedHostPathFmt, uvm.ID),
		}

		if err := uvm.GuestConnection.Modify(ctx, guestReq); err != nil {
			return fmt.Errorf("failed to make guest request to add virtiofs mount: %w", err)
		}
	}

	sharePath := fmt.Sprintf(SharedHostPathCntrFmt, uvm.ID, cntrID)
	for i, mount := range s.Mounts {
		fileInfo, err := os.Stat(mount.Source)
		if err != nil {
			return err
		}

		bindPath := filepath.Join(sharePath, mount.Source)
		dirChain := bindPath
		if !fileInfo.IsDir() {
			dirChain = filepath.Dir(dirChain)
		}
		if err := os.MkdirAll(dirChain, 0700); err != nil {
			return err
		}

		// Bind any OCI mounts under the directory we'll share into the guest, and fix up the OCI spec to search here.
		// We'll be creating the same directory structure in the VM.
		if err := unix.Mount(mount.Source, bindPath, "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("failed to create bind mount from %q to %q: %v", mount.Source, sharePath, err)
		}
		s.Mounts[i].Source = bindPath
	}
	return nil
}
