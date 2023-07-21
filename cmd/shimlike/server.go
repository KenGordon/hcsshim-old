package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/gcs"
	shimapi "github.com/Microsoft/hcsshim/pkg/shimlike/api"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	kubeletApiVersion = "1.26"
	runtimeVersion    = "0.0.1"
	runtimeName       = "Shimlike"
	runtimeApiVersion = "0.0.1"

	gcsPort uint32 = 0x40000000 // The port on which the UVM's GCS server listens
	logPort uint32 = 109        // The port on which the UVM's forwards std streams
)

type RuntimeServer struct {
	VMID         string
	gc           *gcs.GuestConnection // GCS connection
	lc           *winio.HvsockConn    // log connection
	mountmanager *MountManager
	containers   map[string]*Container // map of container ID to container
	grpcServer   *grpc.Server
	sandboxID    string
	sandboxPID   int
	NIC          *shimapi.NIC
}

// connectLog connects to the UVM's log port and stores the connection
// in the RuntimeServer instance
//
// s.VMID must be set before calling this function
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

// ReadLog continuously reads from the log connection and prints to stdout.
func (s *RuntimeServer) readLog() {
	buf := make([]byte, 4096)
	for {
		n, err := s.lc.Read(buf)
		if err != nil {
			logrus.Fatal(err)
		}
		fmt.Print(string(buf[:n]))
	}
}

// acceptGcs accepts a connection from the UVM's GCS port and stores the connection
// in the RuntimeServer instance
//
// s.VMID must be set before calling this function
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
	gcc := &gcs.GuestConnectionConfig{
		Conn:           conn,
		Log:            logrus.NewEntry(logrus.StandardLogger()),
		IoListen:       gcs.HvsockIoListen(ID),
		InitGuestState: &gcs.InitialGuestState{},
	}
	gc, err := gcc.Connect(context.Background(), true)
	if err != nil {
		return err
	}
	s.gc = gc
	return nil
}

func (*RuntimeServer) Version(ctx context.Context, req *shimapi.VersionRequest) (*shimapi.VersionResponse, error) {
	r := &shimapi.VersionResponse{
		Version:           kubeletApiVersion,
		RuntimeName:       runtimeName,
		RuntimeVersion:    runtimeVersion,
		RuntimeApiVersion: runtimeApiVersion,
	}
	return r, nil
}

// RunPodSandbox is a reserved function for setting up the Shimlike.
func (s *RuntimeServer) RunPodSandbox(ctx context.Context, req *shimapi.RunPodSandboxRequest) (*shimapi.RunPodSandboxResponse, error) {
	return &shimapi.RunPodSandboxResponse{}, s.runPodSandbox(ctx, req)
}
func (s *RuntimeServer) StopPodSandbox(ctx context.Context, req *shimapi.StopPodSandboxRequest) (*shimapi.StopPodSandboxResponse, error) {
	for i := range s.containers {
		s.removeContainer(ctx, i)
	}
	go func() { // Goroutine so we can still send the response
		time.Sleep(5 * time.Second)
		s.gc.Close()
		s.lc.Close()
		s.grpcServer.GracefulStop()
	}()
	return &shimapi.StopPodSandboxResponse{}, nil
}
func (s *RuntimeServer) CreateContainer(ctx context.Context, req *shimapi.CreateContainerRequest) (*shimapi.CreateContainerResponse, error) {
	logrus.WithField("request", req).Info("shimlike::CreateContainer")
	id, err := s.createContainer(ctx, req.Config)
	if err != nil {
		return nil, err
	}
	return &shimapi.CreateContainerResponse{ContainerId: id}, nil
}
func (s *RuntimeServer) StartContainer(ctx context.Context, req *shimapi.StartContainerRequest) (*shimapi.StartContainerResponse, error) {
	logrus.WithField("request", req).Info("shimlike::StartContainer")
	_, err := s.startContainer(ctx, req.ContainerId)
	return &shimapi.StartContainerResponse{}, err
}
func (s *RuntimeServer) StopContainer(ctx context.Context, req *shimapi.StopContainerRequest) (*shimapi.StopContainerResponse, error) {
	logrus.WithField("request", req).Info("shimlike::StopContainer")
	return &shimapi.StopContainerResponse{}, s.stopContainer(ctx, req.ContainerId, req.Timeout)
}
func (s *RuntimeServer) RemoveContainer(ctx context.Context, req *shimapi.RemoveContainerRequest) (*shimapi.RemoveContainerResponse, error) {
	logrus.WithField("request", req).Info("shimlike::RemoveContainer")
	return &shimapi.RemoveContainerResponse{}, s.removeContainer(ctx, req.ContainerId)
}
func (s *RuntimeServer) ListContainers(ctx context.Context, req *shimapi.ListContainersRequest) (*shimapi.ListContainersResponse, error) {
	logrus.WithField("request", req).Info("shimlike::ListContainers")
	containers := s.listContainers(ctx, req.Filter)
	return &shimapi.ListContainersResponse{Containers: containers}, nil
}
func (s *RuntimeServer) ContainerStatus(ctx context.Context, req *shimapi.ContainerStatusRequest) (*shimapi.ContainerStatusResponse, error) {
	logrus.WithField("request", req).Info("shimlike::ContainerStatus")
	status, err := s.containerStatus(ctx, req.ContainerId)
	if err != nil {
		return nil, err
	}
	return &shimapi.ContainerStatusResponse{Status: status}, nil
}
func (s *RuntimeServer) UpdateContainerResources(ctx context.Context, req *shimapi.UpdateContainerResourcesRequest) (*shimapi.UpdateContainerResourcesResponse, error) {
	return &shimapi.UpdateContainerResourcesResponse{}, s.updateContainerResources(ctx, req.ContainerId, req.Linux, req.Annotations)
}
func (*RuntimeServer) ReopenContainerLog(ctx context.Context, req *shimapi.ReopenContainerLogRequest) (*shimapi.ReopenContainerLogResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ReopenContainerLog not implemented")
}
func (s *RuntimeServer) ExecSync(ctx context.Context, req *shimapi.ExecSyncRequest) (*shimapi.ExecSyncResponse, error) {
	logrus.WithField("request", req).Info("shimlike::ExecSync")
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.Timeout))
	defer cancel()
	return s.execSync(ctx, req)
}
func (s *RuntimeServer) Exec(ctx context.Context, req *shimapi.ExecRequest) (*shimapi.ExecResponse, error) {
	logrus.WithField("request", req).Info("shimlike::Exec")
	return s.exec(ctx, req)
}
func (s *RuntimeServer) Attach(ctx context.Context, req *shimapi.AttachRequest) (*shimapi.AttachResponse, error) {
	logrus.WithField("request", req).Info("shimlike::Attach")
	return s.attach(ctx, req)
}
func (s *RuntimeServer) ContainerStats(ctx context.Context, req *shimapi.ContainerStatsRequest) (*shimapi.ContainerStatsResponse, error) {
	stats, err := s.containerStats(ctx, req.ContainerId)
	if err != nil {
		return nil, err
	}
	return &shimapi.ContainerStatsResponse{Stats: stats}, nil
}
func (s *RuntimeServer) ListContainerStats(ctx context.Context, req *shimapi.ListContainerStatsRequest) (*shimapi.ListContainerStatsResponse, error) {
	return s.listContainerStats(ctx, req)
}
func (*RuntimeServer) Status(ctx context.Context, req *shimapi.StatusRequest) (*shimapi.StatusResponse, error) {
	return &shimapi.StatusResponse{Status: &shimapi.RuntimeStatus{Conditions: []*shimapi.RuntimeCondition{}}}, nil
}
func (*RuntimeServer) GetContainerEvents(req *shimapi.GetEventsRequest, srv shimapi.RuntimeService_GetContainerEventsServer) error {
	return status.Errorf(codes.Unimplemented, "method GetContainerEvents not implemented")
}
