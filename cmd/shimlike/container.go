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
	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/gcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/hns"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	shimapi "github.com/Microsoft/hcsshim/pkg/shimlike/api"
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
	Container          *shimapi.Container
	ProcessHost        *gcs.Container
	Status             *ContainerStatus
	LogPath            string
	Disks              []*ScsiDisk
	ContainerResources *shimapi.ContainerResources // Container's resources. May be nil.
	Cmds               []*cmd.Cmd                  // All commands running in the container. The first element is the main process.
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
func validateContainerConfig(c *shimapi.ContainerConfig) error {
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

// containerPrefixSearch returns the container with the given ID prefix and true.
// If no container is found, nil and false are returned.
// If multiple containers with the same prefix are found, the first one is returned.
func (s *RuntimeServer) containerPrefixSearch(prefix string) (*Container, bool) {
	if c, ok := s.containers[prefix]; ok {
		return c, true
	}
	for _, c := range s.containers {
		if c.Container.Id[:len(prefix)] == prefix {
			if c.Container.Id == s.sandboxID {
				continue
			}
			return c, true
		}
	}
	return nil, false
}

// runPodSandbox creates and starts a sandbox container in the UVM. This function should only be called once
// in a UVM's lifecycle.
func (s *RuntimeServer) runPodSandbox(ctx context.Context, r *shimapi.RunPodSandboxRequest) error {
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
	doc, err := readSpec()
	if err != nil {
		return err
	}
	applySandboxSpec(doc)

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
	layerPaths := []string{layerPath}
	rootPath, err := s.mountmanager.combineLayers(ctx, layerPaths, scratchDiskPath, id)
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
		Container: &shimapi.Container{
			Id: id,
			Metadata: &shimapi.ContainerMetadata{
				Name:    "sandbox",
				Attempt: 1,
			},
			Image: &shimapi.ImageSpec{
				Image:       "pause",
				Annotations: doc.OciSpecification.Annotations,
			},
			ImageRef:    "", //TODO: What is this?
			State:       shimapi.ContainerState_CONTAINER_CREATED,
			CreatedAt:   time.Now().UnixNano(),
			Labels:      nil,
			Annotations: doc.OciSpecification.Annotations,
		},
		ProcessHost: container,
		LogPath:     "", // TODO: Put this somewhere
		Disks:       []*ScsiDisk{disk, scratchDisk},
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

// createContainer creates a container in the UVM and returns the newly created container's ID
//
// TODO: Non-layer, non-network devices are not supported yet
func (s *RuntimeServer) createContainer(ctx context.Context, c *shimapi.ContainerConfig) (string, error) {
	err := validateContainerConfig(c)
	if err != nil {
		return "", err
	}

	// create the container document
	doc, err := readSpec()
	if err != nil {
		return "", err
	}
	applyContainerSpec(doc)

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
	mountPaths := make([]string, 0, len(c.Mounts))
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
		mountPaths = append(mountPaths, mountPath)
	}

	// mount the scratch disk
	scratchDisk := &ScsiDisk{
		Controller: uint8(c.ScratchDisk.Controller),
		Lun:        uint8(c.ScratchDisk.Lun),
		Partition:  uint64(c.ScratchDisk.Partition),
		Readonly:   false,
	}
	scratchDiskPath, err := s.mountmanager.mountScsi(ctx, scratchDisk)
	if err != nil {
		return "", err
	}
	disks = append(disks, scratchDisk)

	// create the rootfs
	rootPath, err := s.mountmanager.combineLayers(ctx, mountPaths, scratchDiskPath, id)
	if err != nil {
		return "", err
	}
	doc.OciSpecification.Root = &specs.Root{
		Path: rootPath,
	}
	doc.ScratchDirPath = scratchDiskPath + scratchDirSuffix

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
		Container: &shimapi.Container{
			Id: id,
			Metadata: &shimapi.ContainerMetadata{
				Name:    c.Metadata.Name,
				Attempt: c.Metadata.Attempt,
			},
			Image: &shimapi.ImageSpec{
				Image:       c.Image.Image,
				Annotations: c.Image.Annotations,
			},
			ImageRef:    "", //TODO: What is this?
			State:       shimapi.ContainerState_CONTAINER_CREATED,
			CreatedAt:   time.Now().UnixNano(),
			Labels:      c.Labels,
			Annotations: doc.OciSpecification.Annotations,
		},
		ProcessHost: container,
		LogPath:     c.LogPath,
		Disks:       disks,
	}
	if c.Linux != nil {
		s.containers[id].ContainerResources = &shimapi.ContainerResources{
			Linux: c.Linux.Resources,
		}
	}
	return id, nil
}

// startContainer starts a created container in the UVM and returns its pid
func (s *RuntimeServer) startContainer(ctx context.Context, containerID string) (int, error) {
	c, ok := s.containerPrefixSearch(containerID)
	if !ok {
		return -1, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != shimapi.ContainerState_CONTAINER_CREATED {
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
	logrus.Infof("Starting container %s", c.Container.Id)
	cmd.Start()
	c.Container.State = shimapi.ContainerState_CONTAINER_RUNNING
	c.Status = &ContainerStatus{
		StartedAt: time.Now().UnixNano(),
	}
	c.Cmds = append(c.Cmds, &cmd)

	go func() {
		cmd.Wait()
		c.Status.FinishedAt = time.Now().UnixNano()
		c.Status.ExitCode = int32(cmd.ExitState.ExitCode())
		c.Container.State = shimapi.ContainerState_CONTAINER_EXITED
	}()
	return cmd.Process.Pid(), nil
}

// StopContainer stops a running container.
func (s *RuntimeServer) stopContainer(ctx context.Context, containerID string, timeout int64) error {
	c, ok := s.containerPrefixSearch(containerID)
	if !ok {
		return status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != shimapi.ContainerState_CONTAINER_RUNNING {
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
	c.Container.State = shimapi.ContainerState_CONTAINER_EXITED
	c.Status.FinishedAt = time.Now().UnixNano()
	return nil
}

// removeContainer removes a container from the UVM. If the container is not found, this is a no-op
func (s *RuntimeServer) removeContainer(ctx context.Context, containerID string) error {
	c, ok := s.containerPrefixSearch(containerID)
	if !ok {
		return nil
	}
	if c.Container.State == shimapi.ContainerState_CONTAINER_RUNNING {
		s.stopContainer(ctx, c.Container.Id, 0)
	}

	err := c.ProcessHost.Close()
	if err != nil {
		return err
	}

	err = s.mountmanager.removeLayers(ctx, c.Container.Id)
	if err != nil {
		return err
	}

	for _, d := range c.Disks {
		err = s.mountmanager.unmountScsi(ctx, d)
		if err != nil {
			return err
		}
	}

	delete(s.containers, c.Container.Id)
	return nil
}

// listContainers returns a list of all containers
func (s *RuntimeServer) listContainers(ctx context.Context, filter *shimapi.ContainerFilter) []*shimapi.Container {
	containers := make([]*shimapi.Container, 0, len(s.containers))

	for _, c := range s.containers {
		if c.Container.Id == s.sandboxID {
			continue
		}
		if filter != nil {
			add := true
			if (filter.Id == "" || c.ProcessHost.ID() == filter.Id) &&
				(filter.State == nil || c.Container.State == filter.State.State) {
				if filter != nil {
					for k, v := range filter.LabelSelector {
						if c.Container.Labels[k] != v {
							add = false
							break
						}
					}
				}
				if add {
					containers = append(containers, c.Container)
				}
			}
		} else {
			containers = append(containers, c.Container)
		}
	}
	return containers
}

// containerStatus returns the status of a container. If the container is not present, this returns an error
func (s *RuntimeServer) containerStatus(ctx context.Context, containerID string) (*shimapi.ContainerStatus, error) {
	c, ok := s.containerPrefixSearch(containerID)
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}

	mounts := make([]*shimapi.Mount, 0, len(c.Disks))
	for _, d := range c.Disks {
		mounts = append(mounts, &shimapi.Mount{
			Controller: int32(d.Controller),
			Lun:        int32(d.Lun),
			Partition:  int32(d.Partition),
			Readonly:   d.Readonly,
		})
	}
	status := &shimapi.ContainerStatus{
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
func (s *RuntimeServer) execSync(context context.Context, req *shimapi.ExecSyncRequest) (*shimapi.ExecSyncResponse, error) {
	c, ok := s.containerPrefixSearch(req.ContainerId)
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != shimapi.ContainerState_CONTAINER_RUNNING {
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
	exitCode := com.ExitState.ExitCode()

	return &shimapi.ExecSyncResponse{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: int32(exitCode),
	}, nil
}

// exec connects to a named pipe to forward output from an executed command in a container
func (s *RuntimeServer) exec(context context.Context, req *shimapi.ExecRequest) (*shimapi.ExecResponse, error) {
	c, ok := s.containerPrefixSearch(req.ContainerId)
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != shimapi.ContainerState_CONTAINER_RUNNING {
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
		com.Stdin = &PipeReader{pipe: &pipe}
	}
	if req.Stdout {
		com.Stdout = &PipeWriter{pipe: &pipe}
	}
	if req.Stderr {
		com.Stderr = &PipeWriter{pipe: &pipe}
	}
	c.Cmds = append(c.Cmds, com)
	index := len(c.Cmds) - 1
	go func() {
		com.Run()
		c.Cmds[index] = c.Cmds[len(c.Cmds)-1] // Remove the command from the list
		c.Cmds = c.Cmds[:len(c.Cmds)-1]       // by copying the last element to the index and slicing
	}()

	return &shimapi.ExecResponse{}, nil
}

// Attach connects to a named pipe and streams output from a running container
func (s *RuntimeServer) attach(context context.Context, req *shimapi.AttachRequest) (*shimapi.AttachResponse, error) {
	c, ok := s.containerPrefixSearch(req.ContainerId)
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}
	if c.Container.State != shimapi.ContainerState_CONTAINER_RUNNING {
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

	return &shimapi.AttachResponse{}, nil
}

// containerStats returns the stats of a container. If the container is not present, this returns an error
func (s *RuntimeServer) containerStats(ctx context.Context, containerID string) (*shimapi.ContainerStats, error) {
	c, ok := s.containerPrefixSearch(containerID)
	if !ok {
		return nil, status.Error(codes.NotFound, "container not found")
	}
	props, err := c.ProcessHost.PropertiesV2(ctx, hcsschema.PTMemory, hcsschema.PTStatistics, hcsschema.PTCPUGroup)
	if err != nil {
		return nil, err
	}

	stats := &shimapi.ContainerStats{
		Attributes: &shimapi.ContainerAttributes{
			Id:          c.Container.Id,
			Metadata:    c.Container.Metadata,
			Labels:      c.Container.Labels,
			Annotations: c.Container.Annotations,
		},
		Cpu: &shimapi.CpuUsage{
			Timestamp:            props.Statistics.Timestamp.UnixNano(),
			UsageCoreNanoSeconds: &shimapi.UInt64Value{Value: props.Metrics.CPU.Usage.Total},
		},
		Memory: &shimapi.MemoryUsage{
			Timestamp:       props.Statistics.Timestamp.UnixNano(),
			UsageBytes:      &shimapi.UInt64Value{Value: props.Metrics.Memory.Usage.Usage},
			RssBytes:        &shimapi.UInt64Value{Value: props.Metrics.Memory.RSS},
			PageFaults:      &shimapi.UInt64Value{Value: props.Metrics.Memory.PgFault},
			MajorPageFaults: &shimapi.UInt64Value{Value: props.Metrics.Memory.PgMajFault},
		},
		WritableLayer: &shimapi.FilesystemUsage{
			Timestamp: props.Statistics.Timestamp.UnixNano(),
			UsedBytes: &shimapi.UInt64Value{Value: props.Statistics.Storage.WriteSizeBytes},
		},
	}
	return stats, nil
}

func (s *RuntimeServer) listContainerStats(ctx context.Context, req *shimapi.ListContainerStatsRequest) (*shimapi.ListContainerStatsResponse, error) {
	stats := make([]*shimapi.ContainerStats, 0, len(s.containers))
	for _, c := range s.containers {
		if c.Container.Id == s.sandboxID {
			continue
		}
		if req.Filter != nil {
			add := true
			if req.Filter.Id == "" || c.Container.Id == req.Filter.Id {
				for k, v := range req.Filter.LabelSelector {
					if c.Container.Labels[k] != v {
						add = false
						break
					}
				}
				if add {
					stat, err := s.containerStats(ctx, c.Container.Id)
					if err != nil {
						return nil, err
					}
					stats = append(stats, stat)
				}
			}
		} else {
			stat, err := s.containerStats(ctx, c.Container.Id)
			if err != nil {
				return nil, err
			}
			stats = append(stats, stat)
		}
	}
	return &shimapi.ListContainerStatsResponse{Stats: stats}, nil
}
