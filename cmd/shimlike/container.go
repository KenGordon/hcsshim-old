package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Microsoft/go-winio"
	p "github.com/Microsoft/hcsshim/cmd/shimlike/proto"
	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/gcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/hns"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	bundlePath = "/run/gcs/c/%s" // path to the OCI bundle given the container ID
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
	Cmds               []*cmd.Cmd            // All commands running in the container. The first element is the main process.
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
	return nil
}

// runPodSandbox creates and starts a sandbox container in the UVM. This function should only be called once
// in a UVM's lifecycle.
func (s *RuntimeServer) runPodSandbox(ctx context.Context, r *p.RunPodSandboxRequest) error {
	endpoints, err := hns.GetNamespaceEndpoints(r.Nic.NamespaceId)
	if err != nil {
		return err
	}
	if len(endpoints) == 0 {
		return errors.New("no endpoints found for namespace")
	}
	firstEndpoint, err := hns.GetHNSEndpointByID(endpoints[0])
	if err != nil {
		return err
	}

	// Add the network adapter through GCS
	a := &guestresource.LCOWNetworkAdapter{
		NamespaceID:     r.Nic.NamespaceId,
		ID:              r.Nic.Id,
		MacAddress:      firstEndpoint.MacAddress,
		IPAddress:       firstEndpoint.IPAddress.String(),
		PrefixLength:    firstEndpoint.PrefixLength,
		GatewayAddress:  firstEndpoint.GatewayAddress,
		DNSSuffix:       firstEndpoint.DNSSuffix,
		DNSServerList:   firstEndpoint.DNSServerList,
		EnableLowMetric: firstEndpoint.EnableLowMetric,
		EncapOverhead:   firstEndpoint.EncapOverhead,
	}
	m := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeNetwork,
		RequestType:  guestrequest.RequestTypeAdd,
		Settings:     a,
	}
	if err := s.gc.Modify(ctx, &m); err != nil {
		return err
	}
	s.NIC = r.Nic

	// Create the sandbox container
	doc := createSandboxSpec()
	id := generateID()
	doc.OciSpecification.Annotations["io.kubernetes.cri.sandbox-id"] = id
	s.sandboxID = id

	doc.OciSpecification.Windows.Network.NetworkNamespace = s.NIC.NamespaceId

	s.mountmanager = &MountManager{gc: s.gc}
	doc.OciBundlePath = fmt.Sprintf(bundlePath, id)

	scratchDisk := &ScsiDisk{
		Controller: uint8(r.ScratchDisk.Controller),
		Lun:        uint8(r.ScratchDisk.Lun),
		Partition:  uint64(r.ScratchDisk.Partition),
		Readonly:   false,
	}
	scratchDiskPath, err := s.mountmanager.mountScsi(ctx, scratchDisk)
	logrus.WithFields(logrus.Fields{
		"disk": fmt.Sprintf("%+v", scratchDisk),
		"path": scratchDiskPath,
	}).Info("Mounted scratch disk")
	if err != nil {
		return err
	}

	disk := &ScsiDisk{
		Controller: uint8(r.PauseDisk.Controller),
		Lun:        uint8(r.PauseDisk.Lun),
		Partition:  uint64(r.PauseDisk.Partition),
		Readonly:   r.PauseDisk.Readonly,
	}
	layerPath, err := s.mountmanager.mountScsi(ctx, disk)
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
		// Attempt to clean up everything
		s.mountmanager.removeLayers(ctx, id)
		s.mountmanager.unmountScsi(ctx, disk)
		s.mountmanager.unmountScsi(ctx, scratchDisk)
		return err
	}
	s.sandboxID = id
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
	doc.OciSpecification.Windows.Network.NetworkNamespace = s.NIC.NamespaceId

	doc.OciSpecification.Process.Env = make([]string, len(c.Envs))
	for i, v := range c.Envs {
		doc.OciSpecification.Process.Env[i] = v.Key + "=" + v.Value
	}

	for k, v := range c.Annotations {
		if k != "io.kubernetes.cri.container-type" { // don't let the user make a sandbox container
			doc.OciSpecification.Annotations[k] = v
		}
	}
	doc.OciSpecification.Annotations["io.kubernetes.cri.sandbox-id"] = s.sandboxID

	// mount the SCSI disks
	disks := make([]*ScsiDisk, 0, len(c.Mounts))
	for _, m := range c.Mounts {
		disk := &ScsiDisk{
			Controller: uint8(m.Controller),
			Lun:        uint8(m.Lun),
			Partition:  uint64(m.Partition),
			Readonly:   m.Readonly,
		}
		mountPath, err := s.mountmanager.mountScsi(ctx, disk)
		logrus.WithFields(logrus.Fields{
			"disk": fmt.Sprintf("%+v", disk),
			"path": mountPath,
		}).Info("Mounted disk")
		if err != nil {
			return "", err
		}
		disks = append(disks, disk)
	}

	// create the rootfs
	rootPath, err := s.mountmanager.combineLayers(ctx, disks, id)
	if err != nil {
		return "", err
	}
	doc.OciSpecification.Root = &specs.Root{
		Path: rootPath,
	}
	doc.ScratchDirPath = fmt.Sprintf(mountPath+scratchDirSuffix, *s.mountmanager.scratchIndex, id)

	// create the container
	container, err := s.gc.CreateContainer(ctx, id, doc)
	if err != nil {
		// Attempt to clean up on error
		s.mountmanager.removeLayers(ctx, id)
		for _, disk := range disks {
			s.mountmanager.unmountScsi(ctx, disk)
		}
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
	if err != nil {
		return -1, err
	}
	cmd := cmd.Cmd{ // TODO: custom log paths/stream settings (reference exec_hcs.go)
		Host:   c.ProcessHost,
		Stdin:  &PipeReader{},
		Stdout: &PipeWriter{},
		Stderr: &PipeWriter{},
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

// removeContainer removes a container from the UVM. If the container is not found, this is a no-op
func (s *RuntimeServer) removeContainer(ctx context.Context, containerID string) error {
	c := s.containers[containerID]
	if c == nil {
		return nil
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

// listContainers returns a list of all containers
func (s *RuntimeServer) listContainers(ctx context.Context, filter *p.ContainerFilter) []*p.Container {
	containers := make([]*p.Container, 0, len(s.containers))
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
			containers = append(containers, c.Container)
		}
	}
	return containers
}

// containerStatus returns the status of a container. If the container is not present, this returns an error
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

// execSync executes a command synchronously in a container
func (s *RuntimeServer) execSync(context context.Context, req *p.ExecSyncRequest) (*p.ExecSyncResponse, error) {
	c, ok := s.containers[req.ContainerId]
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != p.ContainerState_CONTAINER_RUNNING {
		return nil, status.Error(codes.FailedPrecondition, "cannot exec in container: container must be in a running state")
	}
	if len(req.Cmd) < 1 {
		return nil, status.Error(codes.InvalidArgument, "cannot exec in container: no command specified")
	}

	com := cmd.Command(c.ProcessHost, req.Cmd[0], req.Cmd[1:]...)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	com.Stdout = stdout
	com.Stderr = stderr
	err := com.Run()

	// ExitError is fine, means the command exited with a non-zero exit code
	var exitErr *cmd.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return nil, err
	}
	exitCode, err := com.Process.ExitCode()
	if err != nil {
		return nil, err
	}

	return &p.ExecSyncResponse{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: int32(exitCode),
	}, nil
}

// exec connects to a named pipe to forward output from an executed command in a container
func (s *RuntimeServer) exec(context context.Context, req *p.ExecRequest) (*p.ExecResponse, error) {
	c, ok := s.containers[req.ContainerId]
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != p.ContainerState_CONTAINER_RUNNING {
		return nil, status.Error(codes.FailedPrecondition, "cannot exec in container: container must be in a running state")
	}
	if len(req.Cmd) < 1 {
		return nil, status.Error(codes.InvalidArgument, "cannot exec in container: no command specified")
	}
	if !req.Stdin && !req.Stdout && !req.Stderr {
		return nil, status.Error(codes.InvalidArgument, "cannot exec in container: one of stdin, stdout, or stderr must be true")
	}

	pipe, err := winio.DialPipe(req.Pipe, nil)
	if err != nil {
		return nil, err
	}
	com := cmd.Command(c.ProcessHost, req.Cmd[0], req.Cmd[1:]...)
	if req.Stdin {
		stdin, ok := com.Stdin.(*PipeReader)
		if ok {
			stdin.pipe = &pipe
		}
	}
	if req.Stdout {
		stdout, ok := com.Stdout.(*PipeWriter)
		if ok {
			stdout.pipe = &pipe
		}
	}
	if req.Stderr {
		stderr, ok := com.Stderr.(*PipeWriter)
		if ok {
			stderr.pipe = &pipe
		}
	}
	c.Cmds = append(c.Cmds, com)

	return &p.ExecResponse{}, nil
}

// Attach connects to a named pipe and streams output from a running container
func (s *RuntimeServer) attach(context context.Context, req *p.AttachRequest) (*p.AttachResponse, error) {
	c, ok := s.containers[req.ContainerId]
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != p.ContainerState_CONTAINER_RUNNING {
		return nil, status.Error(codes.FailedPrecondition, "cannot exec in container: container must be in a running state")
	}
	if !req.Stdin && !req.Stdout && !req.Stderr {
		return nil, status.Error(codes.InvalidArgument, "cannot exec in container: one of stdin, stdout, or stderr must be true")
	}

	pipe, err := winio.DialPipe(req.Pipe, nil)
	if err != nil {
		return nil, err
	}
	com := c.Cmds[0]
	if req.Stdin {
		stdin, ok := com.Stdin.(*PipeReader)
		if ok {
			stdin.pipe = &pipe
		}
	}
	if req.Stdout {
		stdout, ok := com.Stdout.(*PipeWriter)
		if ok {
			stdout.pipe = &pipe
		}
	}
	if req.Stderr {
		stderr, ok := com.Stderr.(*PipeWriter)
		if ok {
			stderr.pipe = &pipe
		}
	}

	return &p.AttachResponse{}, nil
}
