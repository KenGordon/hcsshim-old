package main

import (
	"context"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	p "github.com/Microsoft/hcsshim/cmd/shimlike/proto"
	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	kubeletApiVersion = "1.26"
	runtimeVersion    = "0.0.1"
	runtimeName       = "Shimlike"
	runtimeApiVersion = "0.0.1"

	gcsPort uint32 = 0x40000000 // The port on which the UVM's GCS server listens
	logPort uint32 = 1090       // The port on which the UVM's forwards std streams
)

type RuntimeServer struct {
	VMID         string
	gc           *gcs.GuestConnection // GCS connection
	lc           *winio.HvsockConn    // log connection
	mountmanager *MountManager
	containers   map[string]*Container // map of container ID to container
}

// connectLog connects to the UVM's log port and returns the connection
func (s *RuntimeServer) connectLog() error {
	ID, err := guid.FromString(s.VMID)
	if err != nil {
		return err
	}

	logrus.Infof("Connecting to UVM %s:%d", ID, logPort)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := winio.Dial(timeoutCtx, &winio.HvsockAddr{
		VMID:      ID,
		ServiceID: winio.VsockServiceID(logPort),
	})
	if err != nil {
		return err
	}
	logrus.Info("Connected to UVM")
	s.lc = conn
	return nil
}

// acceptGcs accepts and returns a connection from the UVM's GCS port
func (s *RuntimeServer) acceptGcs() error {
	ID, err := guid.FromString(s.VMID)
	if err != nil {
		return err
	}

	logrus.Infof("Accepting GCS connection from UVM %s:%d", ID, gcsPort)
	listener, err := winio.ListenHvsock(&winio.HvsockAddr{
		VMID:      ID,
		ServiceID: winio.VsockServiceID(gcsPort),
	})
	if err != nil {
		return err
	}
	defer listener.Close()

	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	logrus.Info("Accepted GCS connection from UVM")

	// Start the GCS protocol.

	var initGuestState *gcs.InitialGuestState
	gcc := &gcs.GuestConnectionConfig{
		Conn:           conn,
		Log:            logrus.NewEntry(logrus.StandardLogger()),
		IoListen:       gcs.HvsockIoListen(ID),
		InitGuestState: initGuestState,
	}
	gc, err := gcc.Connect(context.Background(), true)
	if err != nil {
		return err
	}
	s.gc = gc
	return nil
}

func (*RuntimeServer) Version(ctx context.Context, req *p.VersionRequest) (*p.VersionResponse, error) {
	r := &p.VersionResponse{
		Version:           kubeletApiVersion,
		RuntimeName:       runtimeName,
		RuntimeVersion:    runtimeVersion,
		RuntimeApiVersion: runtimeApiVersion,
	}
	return r, nil
}

// RunPodSandbox is a reserved function for setting up the Shimlike.
func (s *RuntimeServer) RunPodSandbox(ctx context.Context, req *p.RunPodSandboxRequest) (*p.RunPodSandboxResponse, error) {
	return &p.RunPodSandboxResponse{}, nil
}
func (*RuntimeServer) StopPodSandbox(ctx context.Context, req *p.StopPodSandboxRequest) (*p.StopPodSandboxResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method StopPodSandbox not implemented")
}
func (s *RuntimeServer) CreateContainer(ctx context.Context, req *p.CreateContainerRequest) (*p.CreateContainerResponse, error) {
	logrus.Info("shimlike::CreateContainer")
	id, err := s.createContainer(ctx, req.Config)
	if err != nil {
		return nil, err
	}
	return &p.CreateContainerResponse{ContainerId: id}, nil
}
func (s *RuntimeServer) StartContainer(ctx context.Context, req *p.StartContainerRequest) (*p.StartContainerResponse, error) {
	logrus.Info("shimlike::StartContainer")
	return &p.StartContainerResponse{}, s.startContainer(ctx, req.ContainerId)
}
func (s *RuntimeServer) StopContainer(ctx context.Context, req *p.StopContainerRequest) (*p.StopContainerResponse, error) {
	logrus.Info("shimlike::StopContainer")
	return &p.StopContainerResponse{}, s.stopContainer(ctx, req.ContainerId, req.Timeout)
}
func (s *RuntimeServer) RemoveContainer(ctx context.Context, req *p.RemoveContainerRequest) (*p.RemoveContainerResponse, error) {
	logrus.Info("shimlike::RemoveContainer")
	return &p.RemoveContainerResponse{}, s.removeContainer(ctx, req.ContainerId)
}
func (s *RuntimeServer) ListContainers(ctx context.Context, req *p.ListContainersRequest) (*p.ListContainersResponse, error) {
	logrus.Info("shimlike::ListContainers")
	containers := s.listContainers(ctx, req.Filter)
	containersData := make([]*p.Container, len(containers))
	for i, c := range containers {
		containersData[i] = c.Container
	}
	return &p.ListContainersResponse{Containers: containersData}, nil
}
func (s *RuntimeServer) ContainerStatus(ctx context.Context, req *p.ContainerStatusRequest) (*p.ContainerStatusResponse, error) {
	logrus.Info("shimlike::ContainerStatus")
	status, err := s.containerStatus(ctx, req.ContainerId)
	if err != nil {
		return nil, err
	}
	return &p.ContainerStatusResponse{Status: status}, nil
}
func (*RuntimeServer) UpdateContainerResources(ctx context.Context, req *p.UpdateContainerResourcesRequest) (*p.UpdateContainerResourcesResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UpdateContainerResources not implemented")
}
func (*RuntimeServer) ReopenContainerLog(ctx context.Context, req *p.ReopenContainerLogRequest) (*p.ReopenContainerLogResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ReopenContainerLog not implemented")
}
func (*RuntimeServer) ExecSync(ctx context.Context, req *p.ExecSyncRequest) (*p.ExecSyncResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ExecSync not implemented")
}
func (*RuntimeServer) Exec(ctx context.Context, req *p.ExecRequest) (*p.ExecResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Exec not implemented")
}
func (*RuntimeServer) Attach(ctx context.Context, req *p.AttachRequest) (*p.AttachResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Attach not implemented")
}
func (*RuntimeServer) ContainerStats(ctx context.Context, req *p.ContainerStatsRequest) (*p.ContainerStatsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ContainerStats not implemented")
}
func (*RuntimeServer) ListContainerStats(ctx context.Context, req *p.ListContainerStatsRequest) (*p.ListContainerStatsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListContainerStats not implemented")
}
func (*RuntimeServer) Status(ctx context.Context, req *p.StatusRequest) (*p.StatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Status not implemented")
}
func (*RuntimeServer) CheckpointContainer(ctx context.Context, req *p.CheckpointContainerRequest) (*p.CheckpointContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CheckpointContainer not implemented")
}
func (*RuntimeServer) GetContainerEvents(req *p.GetEventsRequest, srv p.RuntimeService_GetContainerEventsServer) error {
	return status.Errorf(codes.Unimplemented, "method GetContainerEvents not implemented")
}
func (*RuntimeServer) ListMetricDescriptors(ctx context.Context, req *p.ListMetricDescriptorsRequest) (*p.ListMetricDescriptorsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListMetricDescriptors not implemented")
}
func (*RuntimeServer) ListPodSandboxMetrics(ctx context.Context, req *p.ListPodSandboxMetricsRequest) (*p.ListPodSandboxMetricsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListPodSandboxMetrics not implemented")
}
