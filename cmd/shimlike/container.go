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

// ContainerStatus contains auxilliary information needed to complete a ContainerStatus call
type ContainerStatus struct {
	StartedAt  int64
	FinishedAt int64
	ExitCode   int32
	Reason     string // Unused
	Message    string // Unused
}

// Container represents a container running in the UVM
type Container struct {
	Container          *p.Container
	ProcessHost        *gcs.Container
	Status             *ContainerStatus
	LogPath            string
	Disks              []*ScsiDisk
	ContainerResources *p.ContainerResources // Container's resources. May be nil.
	Cmds               []*cmd.Cmd            // All commands running in the container
}

// linuxHostedSystem is passed to the GCS to create a container
type linuxHostedSystem struct {
	SchemaVersion    *hcsschema.Version
	OciBundlePath    string
	OciSpecification *specs.Spec
	ScratchDirPath   string
}

// generateID creates a random ID for a container. This method was copied from
// containerd
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

// runPodSandbox creates and starts a sandbox container in the UVM. This function should only be called once
// in a UVM's lifecycle.
func (s *RuntimeServer) runPodSandbox(ctx context.Context, r *p.RunPodSandboxRequest) error {
	doc := createSandboxSpec()
	id := generateID()
	doc.OciSpecification.Annotations["io.kubernetes.sandbox.id"] = id
	s.sandboxID = id

	s.mountmanager = &MountManager{gc: s.gc}
	doc.OciBundlePath = fmt.Sprintf(bundlePath, id)

	scratchDisk := &ScsiDisk{
		Controller: uint8(r.ScratchController),
		Lun:        uint8(r.ScratchLun),
		Partition:  uint64(r.ScratchPartition),
		Readonly:   false,
	}
	scratchDiskPath, err := s.mountmanager.mountScratch(ctx, scratchDisk)
	logrus.WithFields(logrus.Fields{
		"disk": fmt.Sprintf("%+v", scratchDisk),
		"path": scratchDiskPath,
	}).Info("Mounted scratch disk")
	if err != nil {
		return err
	}

	disk := &ScsiDisk{
		Controller: uint8(r.PauseController),
		Lun:        uint8(r.PauseLun),
		Partition:  uint64(r.PausePartition),
		Readonly:   true,
	}
	layerPath, err := s.mountmanager.mountScsi(ctx, disk, id)
	logrus.WithFields(logrus.Fields{
		"disk": fmt.Sprintf("%+v", disk),
		"path": layerPath,
	}).Info("Mounted sandbox disk")
	if err != nil {
		return err
	}

	// create the rootfs
	disks := []*ScsiDisk{disk}
	rootPath, err := s.mountmanager.combineLayers(ctx, disks, id)
	if err != nil {
		return err
	}
	doc.OciSpecification.Root = &specs.Root{
		Path: rootPath,
	}
	doc.ScratchDirPath = fmt.Sprintf(scratchDiskPath+scratchDirSuffix, id)

	// create the container
	container, err := s.gc.CreateContainer(ctx, id, doc)
	if err != nil {
		return err
	}

	if s.containers == nil {
		s.containers = make(map[string]*Container)
	}
	s.containers[id] = &Container{
		Container: &p.Container{
			Id: id,
			Metadata: &p.ContainerMetadata{
				Name:    "sandbox",
				Attempt: 1,
			},
			Image: &p.ImageSpec{
				Image:       "pause",
				Annotations: doc.OciSpecification.Annotations,
			},
			ImageRef:    "", //TODO: What is this?
			State:       p.ContainerState_CONTAINER_CREATED,
			CreatedAt:   time.Now().UnixNano(),
			Labels:      nil,
			Annotations: doc.OciSpecification.Annotations,
		},
		ProcessHost: container,
		LogPath:     "", // TODO: Put this somewhere
		Disks:       disks,
	}
	pid, err := s.startContainer(ctx, id)
	if err != nil {
		return err
	}
	s.sandboxPID = pid
	return nil
}

func assignNamespaces(spec *specs.Spec, pid int) {
	spec.Linux.Namespaces = []specs.LinuxNamespace{
		{
			Type: specs.PIDNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/pid", pid),
		},
		{
			Type: specs.IPCNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/ipc", pid),
		},
		{
			Type: specs.UTSNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/uts", pid),
		},
		{
			Type: specs.MountNamespace,
		},
		{
			Type: specs.NetworkNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/net", pid),
		},
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

	// create the container document
	doc := createContainerSpec()
	id := generateID()
	doc.OciBundlePath = fmt.Sprintf(bundlePath, id)
	doc.OciSpecification.Process.Args = append(c.Command, c.Args...)
	doc.OciSpecification.Process.Cwd = c.WorkingDir

	if c.Linux != nil {
		doc.OciSpecification.Linux.Resources = marshalLinuxResources(c.Linux.Resources)
	}

	assignNamespaces(doc.OciSpecification, s.sandboxPID)

	doc.OciSpecification.Process.Env = make([]string, len(c.Envs))
	for i, v := range c.Envs {
		doc.OciSpecification.Process.Env[i] = v.Key + "=" + v.Value
	}

	// mount the SCSI disks

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
	doc.ScratchDirPath = fmt.Sprintf(s.mountmanager.scratchDiskPath+scratchDirSuffix, id)
	doc.OciSpecification.Annotations = c.Annotations
	doc.OciSpecification.Annotations["io.kubernetes.sandbox.id"] = s.sandboxID

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
			Annotations: doc.OciSpecification.Annotations,
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

// startContainer starts a created container in the UVM and returns its pid
func (s *RuntimeServer) startContainer(ctx context.Context, containerID string) (int, error) {
	c := s.containers[containerID]
	if c == nil {
		return -1, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != p.ContainerState_CONTAINER_CREATED {
		return -1, status.Error(codes.FailedPrecondition, "cannot start container: container must be in a created state")
	}
	err := c.ProcessHost.Start(ctx)
	if err != nil {
		return -1, err
	}
	cmd := cmd.Cmd{ // TODO: custom log paths/stream settings (reference exec_hcs.go)
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
	c.Cmds = append(c.Cmds, &cmd)

	go cmd.Wait() // TODO: leaking non-zero exit codes
	return cmd.Process.Pid(), nil
}

// StopContainer stops a running container.
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
		for _, cmd := range c.Cmds { // Kill any running commands in the container
			cmd.Process.Close()
			cmd.Process.Kill(ctx)
		}
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
	if c.Container.State == p.ContainerState_CONTAINER_RUNNING {
		s.stopContainer(ctx, containerID, 0)
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
