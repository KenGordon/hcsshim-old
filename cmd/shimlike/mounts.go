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
	mountPath        = "/run/mounts/scsi/m%d" // path to a mounted layer or scratch disk given its index
	rootfsPath       = "/run/gcs/c/%s/rootfs" // path to the rootfs of a container given its ID
	scratchDirSuffix = "/scratch/%s"          // path to a scratch dir relative to a mounted scsi disk
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
	mounts  []*ScsiDisk
	diskMap map[string]*ScsiDisk // Map of "controller lun partition" to ScsiDisk
	gc      *gcs.GuestConnection
}

func firstEmptyIndex(mounts []*ScsiDisk) int {
	for i, m := range mounts {
		if m == nil {
			return i
		}
	}
	return -1
}

// mountLayer mounts a layer on the UVM and returns its mounted path.
// If the layer is already mounted, then it returns the existing mount path, and disk is modified to
// point to the existing disk.
// Populates disk.MountPath
func (m *MountManager) mountScsi(ctx context.Context, disk *ScsiDisk) (string, error) {
	// Check if already mounted
	if d, ok := m.diskMap[fmt.Sprintf("%d %d %d", disk.Controller, disk.Lun, disk.Partition)]; ok {
		disk.MountIndex = d.MountIndex
		d.References++
		return fmt.Sprintf(mountPath, *disk.MountIndex), nil
	}
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeAdd,
	}

	index := firstEmptyIndex(m.mounts)
	mountIndex := index
	if mountIndex == -1 { // No empty index, so append to the end
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

	logrus.WithField("request", fmt.Sprintf("%+v", req)).Infof("Mounting SCSI disk %d %d %d", disk.Controller, disk.Lun, disk.Partition)
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
	if m.diskMap == nil {
		m.diskMap = make(map[string]*ScsiDisk)
	}
	m.diskMap[fmt.Sprintf("%d %d %d", disk.Controller, disk.Lun, disk.Partition)] = disk

	return mountPath, nil
}

// combineLayers combines all mounted layers to create a rootfs for a container and returns its path.
// The scratch disk must NOT be passed in as a layer.
func (m *MountManager) combineLayers(ctx context.Context, layerPaths []string, scratchPath string, containerID string) (string, error) {
	hcsLayers := make([]hcsschema.Layer, 0, len(layerPaths))
	for _, path := range layerPaths {
		hcsLayers = append(hcsLayers, hcsschema.Layer{
			Path: path,
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
			ScratchPath:       fmt.Sprintf(scratchPath+scratchDirSuffix, containerID),
		},
	}
	logrus.WithField("request", fmt.Sprintf("%+v", req)).Infof("Combining layers for container %s", containerID)
	err := m.gc.Modify(ctx, req)
	if err != nil {
		return "", err
	}
	return path, nil
}

// removeLayers removes the rootfs for a container, but doesn't unmount the disks.
func (m *MountManager) removeLayers(ctx context.Context, containerID string) error {
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeCombinedLayers,
		RequestType:  guestrequest.RequestTypeRemove,
		Settings: guestresource.LCOWCombinedLayers{
			ContainerRootPath: fmt.Sprintf(rootfsPath, containerID),
		},
	}
	logrus.WithField("request", fmt.Sprintf("%+v", req)).Infof("Removing layers for container %s", containerID)
	m.gc.Modify(ctx, req)
	return nil
}

// unmountLayer unmounts a layer from the UVM.
// If the layer is referenced by another container, then it is not unmounted, and instead the reference count is decremented.
// Modifies disk.MountPath
func (m *MountManager) unmountScsi(ctx context.Context, disk *ScsiDisk) error {
	if d, ok := m.diskMap[fmt.Sprintf("%d %d %d", disk.Controller, disk.Lun, disk.Partition)]; ok {
		disk = d
	} else {
		return fmt.Errorf("disk is not mounted")
	}
	if disk.References > 1 {
		disk.References--
		logrus.Infof("Not unmounting SCSI disk %d %d %d, referenced by %d other container(s)", disk.Controller, disk.Lun, disk.Partition, disk.References)
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
	logrus.WithField("request", fmt.Sprintf("%+v", req)).Infof("Unmounting SCSI disk %d %d %d", disk.Controller, disk.Lun, disk.Partition)
	err := m.gc.Modify(ctx, req)
	if err != nil {
		return err
	}
	m.mounts[*disk.MountIndex] = nil
	disk.MountIndex = nil

	return nil
}
