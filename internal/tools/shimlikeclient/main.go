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

func acceptPrint(pipe net.Listener) {
	con, err := pipe.Accept()
	if err != nil {
		logrus.Fatal(err)
	}
	defer con.Close()

	buf := make([]byte, 1024)
	for {
		n, err := con.Read(buf)
		if err != nil {
			logrus.Fatal(err)
		}
		fmt.Print(string(buf[:n]))
	}
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
	logrus.Infof("Response: %v", ccResp)
	scResp, err := client.StartContainer(context.Background(), &p.StartContainerRequest{
		ContainerId: ccResp.ContainerId,
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", scResp)

	pipe, err := winio.ListenPipe(`\\.\pipe\slclient\attach`, nil)
	if err != nil {
		logrus.Fatal(err)
	}
	defer pipe.Close()

	go acceptPrint(pipe)

	aResp, err := client.Attach(context.Background(), &p.AttachRequest{
		ContainerId: ccResp.ContainerId,
		Stdout:      true,
		Stderr:      true,
		Pipe:        pipe.Addr().String(),
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", aResp)

	time.Sleep(2 * time.Second)

	pipe2, err := winio.ListenPipe(`\\.\pipe\slclient\exec`, nil)
	if err != nil {
		logrus.Fatal(err)
	}
	defer pipe2.Close()

	go acceptPrint(pipe2)

	eResp, err := client.Exec(context.Background(), &p.ExecRequest{
		Cmd:         []string{"ash", "-c", "for i in $(seq 1 10); do echo $i; sleep 1; done"},
		ContainerId: ccResp.ContainerId,
		Stdout:      true,
		Stderr:      true,
		Pipe:        pipe.Addr().String(),
	})
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Response: %v", eResp)

	time.Sleep(30 * time.Second)
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
