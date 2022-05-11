//go:build windows
// +build windows

package oci

import (
	"encoding/json"
	"strings"

	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

// TODO katiewasnothere: find a better place for this to live
func UpdateSpecWithLayerPaths(spec *specs.Spec, rootfs []*types.Mount) error {
	// TODO katiewasnothere: move all of this stuff out of this package
	if len(rootfs) == 0 {
		// If no mounts are passed via the snapshotter its the callers full
		// responsibility to manage the storage. Just move on without affecting
		// the config.json at all.
		if spec.Windows == nil || len(spec.Windows.LayerFolders) < 2 {
			return errors.Wrap(errdefs.ErrFailedPrecondition, "no Windows.LayerFolders found in oci spec")
		}
	} else if len(rootfs) != 1 {
		return errors.Wrap(errdefs.ErrFailedPrecondition, "Rootfs does not contain exactly 1 mount for the root file system")
	}
	m := rootfs[0]
	if m.Type != "windows-layer" && m.Type != "lcow-layer" {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "unsupported mount type '%s'", m.Type)
	}

	var parentLayerPaths []string
	for _, option := range m.Options {
		if strings.HasPrefix(option, mount.ParentLayerPathsFlag) {
			err := json.Unmarshal([]byte(option[len(mount.ParentLayerPathsFlag):]), &parentLayerPaths)
			if err != nil {
				return errors.Wrap(err, "failed to unmarshal parent layer paths from mount")
			}
		}
	}

	if m.Type == "lcow-layer" {
		// If we are creating LCOW make sure that spec.Windows is filled out before
		// appending layer folders.
		if spec.Windows == nil {
			spec.Windows = &specs.Windows{}
		}
		if spec.Windows.HyperV == nil {
			spec.Windows.HyperV = &specs.WindowsHyperV{}
		}
	} else if spec.Windows.HyperV == nil {
		// This is a Windows Argon make sure that we have a Root filled in.
		if spec.Root == nil {
			spec.Root = &specs.Root{}
		}
	}

	// Append the parents
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, parentLayerPaths...)
	// Append the scratch
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, m.Source)
	return nil
}
