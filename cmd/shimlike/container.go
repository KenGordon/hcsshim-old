package main

import (
	"context"
	"fmt"
	"os"

	p "github.com/Microsoft/hcsshim/cmd/shimlike/proto"
	"github.com/Microsoft/hcsshim/internal/cmd"
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

type linuxHostedSystem struct {
	SchemaVersion    *hcsschema.Version
	OciBundlePath    string
	OciSpecification *specs.Spec
	ScratchDirPath   string
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
				// Namespaces?
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

// createContainer creates a container in the UVM and returns the newly created container's ID
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
	id := c.Metadata.Name + "-" + fmt.Sprint(c.Metadata.Attempt)
	doc.OciBundlePath = fmt.Sprintf(bundlePath, id)
	doc.OciSpecification.Process.Args = append(c.Command, c.Args...)
	doc.OciSpecification.Process.Cwd = c.WorkingDir

	doc.OciSpecification.Process.Env = make([]string, 0, len(c.Envs))
	for i, v := range c.Envs {
		doc.OciSpecification.Process.Env[i] = v.Key + "=" + v.Value
	}

	// mount the SCSI disks
	mountPaths := make([]string, 0, len(c.Mounts))
	scratchDiskPath := ""
	scratchDirPath := ""
	for _, m := range c.Mounts {
		disk := ScsiDisk{
			Controller: uint8(m.Controller),
			Lun:        uint8(m.Lun),
			Partition:  uint64(m.Partition),
			Readonly:   m.Readonly,
		}
		mountPath, err := s.mountmanager.mountScsi(ctx, disk, id)
		logrus.WithFields(logrus.Fields{
			"disk": disk,
		}).Info("Mounted disk")
		if err != nil {
			return "", err
		}
		if !m.Readonly {
			scratchDiskPath = mountPath
		}
		mountPaths = append(mountPaths, mountPath)
	}
	scratchDirPath = fmt.Sprintf(scratchDiskPath+scratchDirSuffix, id)

	// create the rootfs
	rootPath, err := s.mountmanager.combineLayers(ctx, mountPaths, id)
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
	s.containers[id] = container
	return id, nil
}

func (s *RuntimeServer) startContainer(ctx context.Context, containerID string) error {
	c := s.containers[containerID]
	if c == nil {
		return status.Error(codes.NotFound, "container not found")
	}
	err := c.Start(ctx)
	if err != nil {
		return err
	}
	cmd := cmd.Cmd{
		Host:   c,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	cmd.Start()
	go cmd.Wait()
	return nil
}
