package main

import (
	"context"
	"fmt"
	"net"
	"os"
	osExec "os/exec"

	"github.com/Microsoft/go-winio"
	shimapi "github.com/Microsoft/hcsshim/pkg/shimlike/api"
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

func clear() {
	cmd := osExec.Command("cmd", "/c", "cls")
	cmd.Stdout = os.Stdout
	cmd.Run()
}

func doMenu(display string) int {
	clear()
	fmt.Println(`--------Shimlike Client--------
|1.  Version                  |
|2.  RunPodSandbox            |
|3.  StopPodSandbox           |
|4.  CreateContainer          |
|5.  StartContainer           |
|6.  StopContainer            |
|7.  RemoveContainer          |
|8.  ListContainers           |
|9.  ContainerStatus 	      |
|10. UpdateContainerResources |
|11. ExecSync                 |
|12. Exec                     |
|13. Attach                   |
|14. ContainerStats           |
|15. ListContainerStats       |
|16. Status                   |
-------------------------------`)
	fmt.Println(display)
	fmt.Print("Enter your choice: ")
	var input int
	_, err := fmt.Scanln(&input)
	if err != nil {
		return doMenu("Invalid choice")
	}
	return input
}

func run(cCtx *cli.Context) {
	if cCtx.NArg() != 2 {
		logrus.Fatalf("Usage: %s", usage)
	}

	// Connect to the shimlike service
	logrus.Info("Connecting to Shimlike...")

	opts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithContextDialer(pipeDialer)}
	conn, err := grpc.Dial(cCtx.Args().First(), opts...)
	if err != nil {
		logrus.Fatal(err)
	}
	defer conn.Close()

	client := shimapi.NewRuntimeServiceClient(conn)
	choice := doMenu("Connected to Shimlike")
	for {
		var result string
		switch choice {
		case 1:
			result = version(client)
		case 2:
			result = runPodSandbox(client)
		case 3:
			fmt.Println(stopPodSandbox(client))
			return
		case 4:
			result = createContainer(client)
		case 5:
			result = startContainer(client)
		case 6:
			result = stopContainer(client)
		case 7:
			result = removeContainer(client)
		case 8:
			result = listContainers(client)
		case 9:
			result = containerStatus(client)
		case 10:
			result = updateContainerResources(client)
		case 11:
			result = execSync(client)
		case 12:
			result = exec(client)
		case 13:
			result = attach(client)
		case 14:
			// ContainerStats
			result = "Not implemented"
		case 15:
			// ListContainerStats
			result = "Not implemented"
		case 16:
			// Status
			result = "Normal"
		default:
			result = "Invalid choice"
		}
		choice = doMenu(result)
	}
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
