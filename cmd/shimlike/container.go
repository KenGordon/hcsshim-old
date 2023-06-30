package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	p "github.com/Microsoft/hcsshim/cmd/shimlike/proto"
	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/gcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	bundlePath       = "/run/gcs/c/%s" // path to the OCI bundle given the container ID
	scratchDirSuffix = "/scratch/%s"   // path to a scratch dir relative to a mounted scsi disk
)

type ContainerStatus struct {
	StartedAt  int64
	FinishedAt int64
	ExitCode   int32
	Reason     string // Unused
	Message    string // Unused
}

type Container struct {
	Container          *p.Container
	ProcessHost        *gcs.Container
	Status             *ContainerStatus
	LogPath            string
	Disks              []*ScsiDisk
	ContainerResources *p.ContainerResources // Container's resources. May be nil.
}

type linuxHostedSystem struct {
	SchemaVersion    *hcsschema.Version
	OciBundlePath    string
	OciSpecification *specs.Spec
	ScratchDirPath   string
}

func generateID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// validateContainerConfig validates a container config received from the ContainerController
func validateContainerConfig(c *p.ContainerConfig) error {
	if c == nil {
		return status.Error(codes.InvalidArgument, "container config is nil")
	}
	if c.Metadata.Name == "" {
		return status.Error(codes.InvalidArgument, "container config is missing metadata")
	}
	if c.Image.Image == "" {
		return status.Error(codes.InvalidArgument, "container config is missing image name")
	}
	if len(c.Command) == 0 {
		return status.Error(codes.InvalidArgument, "container config is missing command")
	}
	if c.WorkingDir == "" {
		return status.Error(codes.InvalidArgument, "container config is missing working directory")
	}
	if len(c.Mounts) == 0 {
		return status.Error(codes.InvalidArgument, "container config is missing mounts")
	}
	hasScratch := false
	hasRoot := false
	for _, m := range c.Mounts {
		if m.Readonly {
			hasRoot = true
		}
		if !m.Readonly {
			hasScratch = true
		}
	}
	if !hasScratch {
		return status.Error(codes.InvalidArgument, "container config is missing scratch mount")
	}
	if !hasRoot {
		return status.Error(codes.InvalidArgument, "container config is missing root mount")
	}
	return nil
}

// createDefaultContainerDoc creates a default container document for a container.
// All required arguments will be set except the ones checked in validateContainerConfig
// and OciBundlePath.
//
// TODO: This could be put into a json file or something similar to avoid hardcoding these values
func createDefaultContainerDoc() linuxHostedSystem {
	l := linuxHostedSystem{
		SchemaVersion: &hcsschema.Version{Major: 2, Minor: 1},
		OciSpecification: &specs.Spec{
			Version: "1.0.2-dev",
			Process: &specs.Process{
				User: specs.User{
					UID: 0,
					GID: 0,
				},
			},
			Linux: &specs.Linux{
				CgroupsPath: "/sys/fs/cgroup",
				Namespaces: []specs.LinuxNamespace{
					{
						Type: "pid",
					},
					{
						Type: "ipc",
					},
					{
						Type: "uts",
					},
					{
						Type: "mount",
					},
					{
						Type: "network",
					},
				},
				MaskedPaths: []string{
					"/proc/acpi",
					"/proc/asound",
					"/proc/kcore",
					"/proc/keys",
					"/proc/latency_stats",
					"/proc/timer_list",
					"/proc/timer_stats",
					"/proc/sched_debug",
					"/sys/firmware",
					"/proc/scsi",
				},
				ReadonlyPaths: []string{
					"/proc/bus",
					"/proc/fs",
					"/proc/irq",
					"/proc/sys",
					"/proc/sysrq-trigger",
				},
			},
			Mounts: []specs.Mount{
				{Destination: "/proc", Type: "proc", Source: "proc", Options: []string{"nosuid", "noexec", "nodev"}},
				{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
				{Destination: "/dev/pts", Type: "devpts", Source: "devpts", Options: []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"}},
				{Destination: "/dev/shm", Type: "tmpfs", Source: "shm", Options: []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"}},
				{Destination: "/dev/mqueue", Type: "mqueue", Source: "mqueue", Options: []string{"nosuid", "noexec", "nodev"}},
				{Destination: "/sys", Type: "sysfs", Source: "sysfs", Options: []string{"nosuid", "noexec", "nodev"}},
				{Destination: "/sys/fs/cgroup", Type: "cgroup", Source: "cgroup", Options: []string{"nosuid", "noexec", "nodev", "relatime", "ro"}},
			},
		},
	}
	return l
}

// assignLinuxResources assigns the linux resources from the container config to the oci specification
func (d *linuxHostedSystem) assignLinuxResources(c *p.LinuxContainerResources) {
	d.OciSpecification.Linux.Resources.CPU = &specs.LinuxCPU{
		Cpus: c.CpusetCpus,
		Mems: c.CpusetMems,
	}
	if shares := uint64(c.CpuShares); shares != 0 {
		d.OciSpecification.Linux.Resources.CPU.Shares = &shares
	}
	if quota := int64(c.CpuQuota); quota != 0 {
		d.OciSpecification.Linux.Resources.CPU.Quota = &quota
	}
	if period := uint64(c.CpuPeriod); period != 0 {
		d.OciSpecification.Linux.Resources.CPU.Period = &period
	}

	if memory := int64(c.MemoryLimitInBytes); memory != 0 {
		d.OciSpecification.Linux.Resources.Memory = &specs.LinuxMemory{
			Limit: &memory,
		}
	}
}

// createContainer creates a container in the UVM and returns the newly created container's ID
//
// TODO: Non-layer devices are not supported yet
func (s *RuntimeServer) createContainer(ctx context.Context, c *p.ContainerConfig) (string, error) {
	err := validateContainerConfig(c)
	if err != nil {
		return "", err
	}
	if s.mountmanager == nil {
		s.mountmanager = &MountManager{gc: s.gc}
	}

	// create the container document
	doc := createDefaultContainerDoc()
	id := generateID()
	doc.OciBundlePath = fmt.Sprintf(bundlePath, id)
	doc.OciSpecification.Process.Args = append(c.Command, c.Args...)
	doc.OciSpecification.Process.Cwd = c.WorkingDir

	if c.Linux != nil {
		doc.assignLinuxResources(c.Linux.Resources)
	}

	doc.OciSpecification.Process.Env = make([]string, len(c.Envs))
	for i, v := range c.Envs {
		doc.OciSpecification.Process.Env[i] = v.Key + "=" + v.Value
	}

	// mount the SCSI disks
	scratchDiskPath := ""
	scratchDirPath := ""

	disks := make([]*ScsiDisk, 0, len(c.Mounts))
	for _, m := range c.Mounts {
		disk := ScsiDisk{
			Controller: uint8(m.Controller),
			Lun:        uint8(m.Lun),
			Partition:  uint64(m.Partition),
			Readonly:   m.Readonly,
		}
		mountPath, err := s.mountmanager.mountScsi(ctx, &disk, id)
		logrus.WithFields(logrus.Fields{
			"disk": fmt.Sprintf("%+v", disk),
			"path": mountPath,
		}).Info("Mounted disk")
		if err != nil {
			return "", err
		}
		if !m.Readonly {
			scratchDiskPath = mountPath
			scratchDirPath = fmt.Sprintf(scratchDiskPath+scratchDirSuffix, id)
		}
		disks = append(disks, &disk)
	}

	// create the rootfs
	rootPath, err := s.mountmanager.combineLayers(ctx, disks, id)
	if err != nil {
		return "", err
	}
	doc.OciSpecification.Root = &specs.Root{
		Path: rootPath,
	}
	doc.ScratchDirPath = scratchDirPath

	// create the container
	container, err := s.gc.CreateContainer(ctx, id, doc)
	if err != nil {
		return "", err
	}

	if s.containers == nil {
		s.containers = make(map[string]*Container)
	}
	s.containers[id] = &Container{
		Container: &p.Container{
			Id: id,
			Metadata: &p.ContainerMetadata{
				Name:    c.Metadata.Name,
				Attempt: c.Metadata.Attempt,
			},
			Image: &p.ImageSpec{
				Image:       c.Image.Image,
				Annotations: c.Image.Annotations,
			},
			ImageRef:    "", //TODO: What is this?
			State:       p.ContainerState_CONTAINER_CREATED,
			CreatedAt:   time.Now().UnixNano(),
			Labels:      c.Labels,
			Annotations: c.Annotations,
		},
		ProcessHost: container,
		LogPath:     c.LogPath,
		Disks:       disks,
	}
	if c.Linux != nil {
		s.containers[id].ContainerResources = &p.ContainerResources{
			Linux: c.Linux.Resources,
		}
	}
	return id, nil
}

// startContainer starts a created container in the UVM
func (s *RuntimeServer) startContainer(ctx context.Context, containerID string) error {
	c := s.containers[containerID]
	if c == nil {
		return status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != p.ContainerState_CONTAINER_CREATED {
		return status.Error(codes.FailedPrecondition, "cannot start container: container must be in a created state")
	}
	err := c.ProcessHost.Start(ctx)
	if err != nil {
		return err
	}
	cmd := cmd.Cmd{ // TODO: custom log paths/stream settings
		Host:   c.ProcessHost,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	cmd.Start()
	c.Container.State = p.ContainerState_CONTAINER_RUNNING
	c.Status = &ContainerStatus{
		StartedAt: time.Now().UnixNano(),
	}

	go cmd.Wait() // TODO: leaking non-zero exit codes
	return nil
}

func (s *RuntimeServer) stopContainer(ctx context.Context, containerID string, timeout int64) error {
	c := s.containers[containerID]
	if c == nil {
		return status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != p.ContainerState_CONTAINER_RUNNING {
		return status.Error(codes.FailedPrecondition, "cannot stop container: container must be in a running state")
	}
	err := c.ProcessHost.Shutdown(ctx)
	if err != nil {
		return err
	}
	go func() {
		time.Sleep(time.Duration(timeout) * time.Second)
		c.ProcessHost.Terminate(ctx)
	}()
	c.Container.State = p.ContainerState_CONTAINER_EXITED
	c.Status.FinishedAt = time.Now().UnixNano()
	return nil
}

func (s *RuntimeServer) removeContainer(ctx context.Context, containerID string) error {
	c := s.containers[containerID]
	if c == nil {
		return status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != p.ContainerState_CONTAINER_EXITED {
		return status.Error(codes.FailedPrecondition, "cannot delete container: container must be in a stopped state")
	}

	err := c.ProcessHost.Close()
	if err != nil {
		return err
	}

	err = s.mountmanager.removeLayers(ctx, containerID)
	if err != nil {
		return err
	}

	for _, d := range c.Disks {
		err = s.mountmanager.unmountScsi(ctx, d)
		if err != nil {
			return err
		}
	}

	delete(s.containers, containerID)
	return nil
}

func (s *RuntimeServer) listContainers(ctx context.Context, filter *p.ContainerFilter) []*Container {
	containers := make([]*Container, 0, len(s.containers))
	skipID := filter.Id == ""
	skipState := filter.State == nil

	for _, c := range s.containers {
		if (c.ProcessHost.ID() == filter.Id || skipID) &&
			(c.Container.State == filter.State.State || skipState) {
			for k, v := range filter.LabelSelector {
				if c.Container.Labels[k] != v {
					continue
				}
			}
			containers = append(containers, c)
		}
	}

	return containers
}

func (s *RuntimeServer) containerStatus(ctx context.Context, containerID string) (*p.ContainerStatus, error) {
	c := s.containers[containerID]
	if c == nil {
		return nil, status.Error(codes.NotFound, "container not found")
	}

	mounts := make([]*p.Mount, 0, len(c.Disks))
	for _, d := range c.Disks {
		mounts = append(mounts, &p.Mount{
			Controller: int32(d.Controller),
			Lun:        int32(d.Lun),
			Partition:  int32(d.Partition),
			Readonly:   d.Readonly,
		})
	}
	status := &p.ContainerStatus{
		Id:          c.Container.Id,
		Metadata:    c.Container.Metadata,
		State:       c.Container.State,
		CreatedAt:   c.Container.CreatedAt,
		StartedAt:   c.Status.StartedAt,
		FinishedAt:  c.Status.FinishedAt,
		ExitCode:    c.Status.ExitCode,
		Image:       c.Container.Image,
		ImageRef:    c.Container.ImageRef,
		Reason:      c.Status.Reason,
		Message:     c.Status.Message,
		Labels:      c.Container.Labels,
		Annotations: c.Container.Annotations,
		Mounts:      mounts,
		LogPath:     c.LogPath,
		Resources:   c.ContainerResources,
	}
	return status, nil
}
