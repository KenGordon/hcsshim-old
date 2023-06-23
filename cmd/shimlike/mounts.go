package main

import (
	"context"
	"fmt"

	"github.com/Microsoft/hcsshim/internal/gcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
)

const (
	mountPath  = "/run/mounts/scsi/m%d" // path to a mounted layer or scratch disk given its index
	rootfsPath = "/run/gcs/c/%s/rootfs" // path to the rootfs of a container given its ID
)

// Represents an unmounted, attached SCSI disk in the UVM
type ScsiDisk struct {
	Controller uint8
	Lun        uint8
	Partition  uint64
	Readonly   bool // False if scratch disk
}

type MountManager struct {
	// A list of mounts. If an index is nil, then that index is available to be mounted on
	mounts []*ScsiDisk
	gc     *gcs.GuestConnection
}

func firstEmptyIndex(mounts []*ScsiDisk) int {
	for i, m := range mounts {
		if m == nil {
			return i
		}
	}
	return -1
}

// TODO: Add some kind of validation of mounts
// mountLayer mounts a layer on the UVM and returns its mounted path
func (m *MountManager) mountScsi(ctx context.Context, disk ScsiDisk, containerID string) (string, error) {
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeAdd,
	}

	index := firstEmptyIndex(m.mounts)
	mountIndex := index
	if mountIndex == -1 {
		mountIndex = len(m.mounts)
	}

	mountPath := fmt.Sprintf(mountPath, mountIndex)

	req.Settings = guestresource.LCOWMappedVirtualDisk{
		MountPath:        mountPath,
		Controller:       disk.Controller,
		Lun:              disk.Lun,
		Partition:        disk.Partition,
		ReadOnly:         disk.Readonly,
		Encrypted:        false,
		Options:          []string{},
		EnsureFilesystem: true,
		Filesystem:       "ext4",
	}

	err := m.gc.Modify(ctx, req)
	if err != nil {
		return "", err
	}

	if index == -1 {
		m.mounts = append(m.mounts, &disk)
	} else {
		m.mounts[index] = &disk
	}

	return mountPath, nil
}

// combineLayers combines all mounted layers to create a rootfs for a container and return its path
func (m *MountManager) combineLayers(ctx context.Context, layerPaths []string, containerID string) (string, error) {
	// Validate that we have a max of one scratch disk
	foundScratch := false
	scratchPath := ""
	for _, m := range m.mounts {
		if m != nil && !m.Readonly {
			if foundScratch {
				return "", fmt.Errorf("found more than one scratch disk")
			}
			foundScratch = true
		}
	}
	hcsLayers := make([]hcsschema.Layer, len(layerPaths))
	for i, l := range layerPaths {
		hcsLayers[i] = hcsschema.Layer{
			Path: l,
		}
	}
	path := fmt.Sprintf(rootfsPath, containerID)
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeCombinedLayers,
		RequestType:  guestrequest.RequestTypeAdd,
		Settings: guestresource.LCOWCombinedLayers{
			ContainerID:       containerID,
			ContainerRootPath: path,
			Layers:            hcsLayers,
			ScratchPath:       scratchPath,
		},
	}
	err := m.gc.Modify(ctx, req)
	if err != nil {
		return "", err
	}
	return path, nil
}
