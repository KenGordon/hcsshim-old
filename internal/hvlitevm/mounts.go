package hvlitevm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	virtTag = "hvl"
)

func isHostDevice(m string) bool {
	if m == "/dev" {
		return true
	}

	if strings.HasPrefix(m, "/dev/") {
		s, err := os.Stat(m)
		if err != nil {
			return false
		}
		if s.Mode().IsRegular() {
			return false
		}
		return true
	}
	return false
}

var systemMountPrefixes = []string{"/proc", "/sys"}

func isSystemMount(m string) bool {
	for _, p := range systemMountPrefixes {
		if m == p || strings.HasPrefix(m, p+"/") {
			return true
		}
	}
	return false
}

// ensureDestinationExists will recursively create a given mountpoint. If directories
// are created, their permissions are initialized to mountPerm
func ensureDestinationExists(source, destination string) error {
	fileInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("could not stat source location %v: %v", source, err)
	}

	targetPathParent, _ := filepath.Split(destination)
	if err := os.MkdirAll(targetPathParent, 0755); err != nil {
		return fmt.Errorf("could not create parent directory %v: %v", targetPathParent, err)
	}

	if fileInfo.IsDir() {
		if err := os.Mkdir(destination, 0755); !os.IsExist(err) {
			return err
		}
	} else {
		file, err := os.OpenFile(destination, os.O_CREATE, 0755)
		if err != nil {
			return err
		}

		file.Close()
	}
	return nil
}

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

	if err := vfs.SetVirtiofsMount(virtTag, sharePath); err != nil {
		return "", err
	}

	return sharePath, nil
}

func (uvm *UtilityVM) setupMounts(ctx context.Context, cntrID string, s *specs.Spec) error {
	log.G(ctx).Infof("Setting up mounts for %s", cntrID)

	// Need to make a guest req to actually mount the virtiofs mount if we haven't already. No support over ttrpc for
	// hot add so creation and the guestreq to mount happen at different stages, just need to be mindful of this.
	if len(s.Mounts) != 0 && atomic.CompareAndSwapUint32(&uvm.sharedFSMounted, 0, 1) {
		guestReq := guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeVirtiofs,
			RequestType:  guestrequest.RequestTypeAdd,
		}

		guestReq.Settings = guestresource.VirtiofsMappedDir{
			Source:    virtTag,
			MountPath: fmt.Sprintf(SharedHostPathFmt, uvm.ID),
		}

		if err := uvm.GuestConnection.Modify(ctx, guestReq); err != nil {
			return fmt.Errorf("failed to make guest request to add virtiofs mount: %w", err)
		}
	}

	sharePath := fmt.Sprintf(SharedHostPathCntrFmt, uvm.ID, cntrID)
	log.G(ctx).Info(s.Mounts)
	for i, mount := range s.Mounts {
		// Skip mounting certain system paths from the source on the host side
		// into the container as it does not make sense to do so.
		if isSystemMount(mount.Source) {
			continue
		}

		if mount.Type != "bind" {
			continue
		}

		if mount.Destination == "/dev/shm" {
			continue
		}

		if isHostDevice(mount.Destination) {
			continue
		}

		bindPath := filepath.Join(sharePath, mount.Source)
		if err := ensureDestinationExists(mount.Source, bindPath); err != nil {
			return err
		}

		// Bind any OCI mounts under the directory we'll share into the guest, and fix up the OCI spec to search here.
		// We'll be creating the same directory structure in the VM.
		if err := unix.Mount(mount.Source, bindPath, "bind", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("failed to create bind mount from %q to %q: %v", mount.Source, bindPath, err)
		}
		s.Mounts[i].Source = bindPath
	}
	return nil
}
