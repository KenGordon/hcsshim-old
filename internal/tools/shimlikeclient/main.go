package main

import (
	"context"
	"fmt"
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

	nic := &p.NIC{}

	fmt.Print("Namespace ID: ")
	fmt.Scan(&nic.NamespaceId)

	fmt.Print("NIC ID: ")
	fmt.Scan(&nic.Id)

	client := p.NewRuntimeServiceClient(conn)
	_, err = client.RunPodSandbox(context.Background(), &p.RunPodSandboxRequest{
		PauseDisk: &p.Mount{
			Controller: 0,
			Lun:        1,
			Partition:  0,
			Readonly:   true,
		},
		ScratchDisk: &p.Mount{
			Controller: 0,
			Lun:        3,
			Partition:  0,
			Readonly:   false,
		},
		Nic: nic,
	})
	if err != nil {
		logrus.Fatal(err)
	}

	numContainers := 3
	containers := make([]string, 0, numContainers)
	for i := 0; i < numContainers; i++ {
		ccResp, err := client.CreateContainer(context.Background(), &p.CreateContainerRequest{
			Config: &p.ContainerConfig{
				Metadata: &p.ContainerMetadata{
					Name:    "alpine",
					Attempt: 1,
				},
				Image: &p.ImageSpec{
					Image: "alpine",
				},
				Command:    []string{"ash", "-c", "apk add iputils && ping microsoft.com"},
				WorkingDir: "/",
				Envs: []*p.KeyValue{
					{Key: "PATH", Value: "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
					{Key: "TERM", Value: "xterm"},
				},
				Mounts: []*p.Mount{
					{Controller: 0, Lun: 2, Partition: 0, Readonly: true},
				},
			},
		})
		if err != nil {
			logrus.Fatal(err)
		}
		containers = append(containers, ccResp.ContainerId)
		logrus.Infof("Response: %v", ccResp)
		scResp, err := client.StartContainer(context.Background(), &p.StartContainerRequest{
			ContainerId: ccResp.ContainerId,
		})
		if err != nil {
			logrus.Fatal(err)
		}
		logrus.Infof("Response: %v", scResp)
	}

	time.Sleep(5 * time.Second)

	for _, container := range containers {
		sResp, err := client.StopContainer(context.Background(), &p.StopContainerRequest{
			ContainerId: container,
		})
		if err != nil {
			logrus.Fatal(err)
		}
		logrus.Infof("Response: %v", sResp)

		time.Sleep(2 * time.Second)

		rmResp, err := client.RemoveContainer(context.Background(), &p.RemoveContainerRequest{
			ContainerId: container,
		})
		if err != nil {
			logrus.Fatal(err)
		}
		logrus.Infof("Response: %v", rmResp)
	}

	time.Sleep(2 * time.Second)

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
