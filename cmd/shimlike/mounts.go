package main

import (
	"context"
	"fmt"

	"github.com/Microsoft/hcsshim/internal/gcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/sirupsen/logrus"
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
	MountIndex *int // Populated by mountScsi
	References int
}

type MountManager struct {
	// A list of mounts for the UVM. If an index is nil, then that index is available to be mounted on.
	// The first index is always the scratch disk for the UVM.
	mounts          []*ScsiDisk
	mountPath       map[string]*ScsiDisk // Map of "controller lun partition" to ScsiDisk
	gc              *gcs.GuestConnection
	scratchDiskPath string
}

func firstEmptyIndex(mounts []*ScsiDisk) int {
	for i, m := range mounts {
		if m == nil {
			return i
		}
	}
	return -1
}

// mountScratch mounts the scratch disk on the UVM and returns its mounted path.
// This function must be called before mounting any other SCSI disks.
func (m *MountManager) mountScratch(ctx context.Context, disk *ScsiDisk) (string, error) {
	if disk.Readonly {
		return "", fmt.Errorf("scratch disk must not be readonly")
	}

	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeAdd,
	}

	mountPath := fmt.Sprintf(mountPath, 0)

	req.Settings = guestresource.LCOWMappedVirtualDisk{
		MountPath:        mountPath,
		Controller:       disk.Controller,
		Lun:              disk.Lun,
		Partition:        disk.Partition,
		ReadOnly:         false,
		Encrypted:        false,
		Options:          []string{},
		EnsureFilesystem: true,
		Filesystem:       "ext4",
	}

	err := m.gc.Modify(ctx, req)
	if err != nil {
		return "", err
	}

	m.mounts = append(m.mounts, disk)
	disk.MountIndex = new(int)
	disk.References++
	return mountPath, nil
}

// TODO: Add some kind of validation of mounts
// mountLayer mounts a layer on the UVM and returns its mounted path.
// If the layer is already mounted, then it returns the existing mount path, and disk is modified to
// point to the existing disk.
// Modifies disk.MountPath
func (m *MountManager) mountScsi(ctx context.Context, disk *ScsiDisk, containerID string) (string, error) {
	if d, ok := m.mountPath[fmt.Sprintf("%d %d %d", disk.Controller, disk.Lun, disk.Partition)]; ok {
		disk = d
		disk.References++
		return fmt.Sprintf(mountPath, *disk.MountIndex), nil
	}
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
		m.mounts = append(m.mounts, disk)
	} else {
		m.mounts[index] = disk
	}
	disk.MountIndex = &mountIndex
	disk.References++
	return mountPath, nil
}

// combineLayers combines all mounted layers to create a rootfs for a container and return its path.
// The scratch path must NOT be passed in as a layer.
func (m *MountManager) combineLayers(ctx context.Context, layers []*ScsiDisk, containerID string) (string, error) {
	// Scratch disk is index 0, as it is always mounted before any container layers
	scratchPath := fmt.Sprintf(mountPath, 0)

	hcsLayers := make([]hcsschema.Layer, 0, len(layers))
	for _, m := range layers {
		hcsLayers = append(hcsLayers, hcsschema.Layer{
			Path: fmt.Sprintf(mountPath, *m.MountIndex),
		})
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
	logrus.WithField("request", fmt.Sprintf("%+v", req)).Infof("Combining layers for container %s", containerID)
	err := m.gc.Modify(ctx, req)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (m *MountManager) removeLayers(ctx context.Context, containerID string) error {
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeCombinedLayers,
		RequestType:  guestrequest.RequestTypeRemove,
		Settings: guestresource.LCOWCombinedLayers{
			ContainerRootPath: fmt.Sprintf(rootfsPath, containerID),
		},
	}
	m.gc.Modify(ctx, req)
	return nil
}

// unmountLayer unmounts a layer from the UVM.
// If the layer is referenced by another container, then it is not unmounted.
// Modifies disk.MountPath
func (m *MountManager) unmountScsi(ctx context.Context, disk *ScsiDisk) error {
	if disk.References > 1 {
		disk.References--
		return nil
	}
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeRemove,
	}
	req.Settings = guestresource.LCOWMappedVirtualDisk{
		MountPath:  fmt.Sprintf(mountPath, *disk.MountIndex),
		Lun:        disk.Lun,
		Partition:  disk.Partition,
		Controller: disk.Controller,
	}
	err := m.gc.Modify(ctx, req)
	if err != nil {
		return err
	}
	m.mounts[*disk.MountIndex] = nil
	disk.MountIndex = nil

	return nil
}
