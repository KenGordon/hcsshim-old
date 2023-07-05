package main

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
	p "github.com/Microsoft/hcsshim/internal/tools/shimlikeclient/proto"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

var (
	usage = "shimlikeclient <pipe address> <UVM ID>"
)

func pipeDialer(ctx context.Context, addr string) (net.Conn, error) {
	return winio.DialPipe(addr, nil)
}

func run(cCtx *cli.Context) {
	if cCtx.NArg() != 2 {
		logrus.Fatalf("Usage: %s", usage)
	}

	// Connect to the shimlike service
	logrus.Info("Connecting to Shimlike")

	opts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithContextDialer(pipeDialer)}
	conn, err := grpc.Dial(cCtx.Args().First(), opts...)
	if err != nil {
		logrus.Fatal(err)
	}
	defer conn.Close()
	logrus.Info("Connected to Shimlike")

	client := p.NewRuntimeServiceClient(conn)
	_, err = client.RunPodSandbox(context.Background(), &p.RunPodSandboxRequest{})
	if err != nil {
		logrus.Fatal(err)
	}

	ccResp, err := client.CreateContainer(context.Background(), &p.CreateContainerRequest{
		Config: &p.ContainerConfig{
			Metadata: &p.ContainerMetadata{
				Name:    "alpine",
				Attempt: 1,
			},
			Image: &p.ImageSpec{
				Image: "alpine",
			},
			Command:    []string{"ash", "-c", "while true; do echo hello; sleep 1; done"},
			WorkingDir: "/",
			Envs: []*p.KeyValue{
				{Key: "PATH", Value: "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
				{Key: "TERM", Value: "xterm"},
			},
			Mounts: []*p.Mount{
				{Controller: 0, Lun: 1, Partition: 0, Readonly: true},
				{Controller: 0, Lun: 2, Partition: 0, Readonly: false},
			},
		},
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", ccResp)

	scResp, err := client.StartContainer(context.Background(), &p.StartContainerRequest{
		ContainerId: ccResp.ContainerId,
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", scResp)

	time.Sleep(1 * time.Second)

	csResp, err := client.ContainerStatus(context.Background(), &p.ContainerStatusRequest{
		ContainerId: ccResp.ContainerId,
		Verbose:     false,
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %+v", csResp.Status)

	time.Sleep(4 * time.Second)

	ucResp, err := client.UpdateContainerResources(context.Background(), &p.UpdateContainerResourcesRequest{
		ContainerId: ccResp.ContainerId,
		Linux: &p.LinuxContainerResources{
			CpuShares:          50,
			MemoryLimitInBytes: 512 * 1024 * 1024,
		},
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", ucResp)

	time.Sleep(4 * time.Second)

	stopResp, err := client.StopContainer(context.Background(), &p.StopContainerRequest{
		ContainerId: ccResp.ContainerId,
		Timeout:     1,
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", stopResp)

	csResp, err = client.ContainerStatus(context.Background(), &p.ContainerStatusRequest{
		ContainerId: ccResp.ContainerId,
		Verbose:     false,
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %+v", csResp.Status)

	rcResp, err := client.RemoveContainer(context.Background(), &p.RemoveContainerRequest{
		ContainerId: ccResp.ContainerId,
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", rcResp)

	spResp, err := client.StopPodSandbox(context.Background(), &p.StopPodSandboxRequest{})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", spResp)
}

func main() {
	app := cli.App{
		Name:      "shimlike client",
		Usage:     "Connect to the shimlike service",
		ArgsUsage: usage,
		Action:    run,
	}
	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
