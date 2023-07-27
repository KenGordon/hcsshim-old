package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	shimapi "github.com/Microsoft/hcsshim/pkg/shimlike/api"
)

// readConnUntilSigint is used by exec and attach to read from and write to
// the connection. It accepts a bool signifying whether or not to send Stdin
// and a channel that is used to signal when the connection
// is ready to be used. If the connection yields false, the
// function returns immediately.
func readConnUntilSigint(conn net.Conn, in bool, ready <-chan bool) { // TODO: Signals handling fails when in is true
	ok := <-ready
	if !ok {
		return
	}
	sigint := make(chan os.Signal, 1)
	errored := make(chan error, 1)
	signal.Notify(sigint, os.Interrupt, syscall.SIGINT)
	buf := make([]byte, 1024)
	go func() {
		for {
			n, err := conn.Read(buf)
			if err != nil {
				signal.Stop(sigint)
				errored <- err
				return
			}
			fmt.Print(string(buf[:n]))
		}
	}()
	if in {
		go func() {
			for {
				text, err := readLine()
				if err != nil {
					//signal.Stop(sigint)
					errored <- err
					return
				}
				_, err = conn.Write([]byte(text + "\n"))
				if err != nil {
					//signal.Stop(sigint)
					errored <- err
					return
				}
			}
		}()
	}
	select {
	case <-sigint:
	case <-errored:
	}
	signal.Stop(sigint)
}

// readLine reads a full line from stdin, removes the newline and the possible
// carriage return, and returns the string
func readLine() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.Split(text[:len(text)-1], "\r")[0], nil
}

// Split a string by spaces except when inside quotes
func quotedSplit(input string) []string {
	var result []string
	var currentToken string
	insideQuotes := false

	for _, char := range input {
		if char == '"' {
			insideQuotes = !insideQuotes
			continue
		}

		if char == ' ' && !insideQuotes {
			if currentToken != "" {
				result = append(result, currentToken)
				currentToken = ""
			}
		} else {
			currentToken += string(char)
		}
	}

	// Add the last token after the loop ends
	if currentToken != "" {
		result = append(result, currentToken)
	}

	return result
}

func version(client shimapi.RuntimeServiceClient) string {
	version, err := client.Version(context.Background(), &shimapi.VersionRequest{})
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return fmt.Sprintf(`Runtime API Version: %v
Runtime Name: %v
Runtime Version: %v
Runtime API Version: %v`, version.Version, version.RuntimeName, version.RuntimeVersion, version.RuntimeApiVersion)
}

func runPodSandbox(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.RunPodSandboxRequest{
		PauseDisk: &shimapi.Mount{
			Readonly: true,
		},
		ScratchDisk: &shimapi.Mount{
			Readonly: false,
		},
		Nic: &shimapi.NIC{},
	}

	fmt.Print("Enter pause container mount disk controller: ")
	_, err := fmt.Scanln(&req.PauseDisk.Lun)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter pause container mount disk LUN: ")
	_, err = fmt.Scanln(&req.PauseDisk.Lun)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter pause container mount disk partition: ")
	_, err = fmt.Scanln(&req.PauseDisk.Partition)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter sandbox scratch disk controller: ")
	_, err = fmt.Scanln(&req.ScratchDisk.Controller)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter sandbox scratch disk LUN: ")
	_, err = fmt.Scanln(&req.ScratchDisk.Lun)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter sandbox scratch disk partition: ")
	_, err = fmt.Scanln(&req.ScratchDisk.Partition)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter NIC namespace ID: ")
	_, err = fmt.Scanln(&req.Nic.NamespaceId)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter NIC ID: ")
	_, err = fmt.Scanln(&req.Nic.Id)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	_, err = client.RunPodSandbox(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return "Success"
}

func stopPodSandbox(client shimapi.RuntimeServiceClient) string {
	_, err := client.StopPodSandbox(context.Background(), &shimapi.StopPodSandboxRequest{})
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return "Success"
}

func createContainer(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.CreateContainerRequest{
		Config: &shimapi.ContainerConfig{
			Image:      &shimapi.ImageSpec{},
			Command:    []string{""},
			WorkingDir: "/",
			Envs: []*shimapi.KeyValue{
				{Key: "PATH", Value: "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
				{Key: "TERM", Value: "xterm"},
			},
			Mounts:      []*shimapi.Mount{},
			ScratchDisk: &shimapi.Mount{},
		},
	}

	fmt.Print("Enter image name: ")
	text, err := readLine()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	req.Config.Image.Image = text
	req.Config.Metadata = &shimapi.ContainerMetadata{
		Name:    req.Config.Image.Image,
		Attempt: 1,
	}

	fmt.Print("Enter command: ")
	_, err = fmt.Scanln(&req.Config.Command[0])
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter arguments: ")
	text, err = readLine()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	req.Config.Args = quotedSplit(text)

	var mounts int
	fmt.Print("Enter number of mounts (not including scratch): ")
	_, err = fmt.Scanln(&mounts)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	for i := 0; i < mounts; i++ {
		mount := &shimapi.Mount{
			Readonly: true,
		}
		fmt.Printf("Enter mount %v controller: ", i)
		_, err = fmt.Scanln(&mount.Controller)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}

		fmt.Printf("Enter mount %v LUN: ", i)
		_, err = fmt.Scanln(&mount.Lun)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}

		fmt.Printf("Enter mount %v partition: ", i)
		_, err = fmt.Scanln(&mount.Partition)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}

		req.Config.Mounts = append(req.Config.Mounts, mount)
	}

	fmt.Print("Enter scratch disk controller: ")
	_, err = fmt.Scanln(&req.Config.ScratchDisk.Controller)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter scratch disk LUN: ")
	_, err = fmt.Scanln(&req.Config.ScratchDisk.Lun)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter scratch disk partition: ")
	_, err = fmt.Scanln(&req.Config.ScratchDisk.Partition)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	resp, err := client.CreateContainer(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return "Container " + resp.ContainerId + " created"
}

func startContainer(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.StartContainerRequest{}

	fmt.Print("Enter container ID: ")
	_, err := fmt.Scanln(&req.ContainerId)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	_, err = client.StartContainer(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return "Success"
}

func stopContainer(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.StopContainerRequest{}

	fmt.Print("Enter container ID: ")
	_, err := fmt.Scanln(&req.ContainerId)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	_, err = client.StopContainer(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return "Success"
}

func removeContainer(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.RemoveContainerRequest{}

	fmt.Print("Enter container ID: ")
	_, err := fmt.Scanln(&req.ContainerId)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	_, err = client.RemoveContainer(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return "Success"
}

func listContainers(client shimapi.RuntimeServiceClient) string {
	list := "ID\tIMAGE\tSTATE"

	resp, err := client.ListContainers(context.Background(), &shimapi.ListContainersRequest{})
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	for _, container := range resp.Containers {
		var state string
		switch container.State {
		case shimapi.ContainerState_CONTAINER_CREATED:
			state = "Created"
		case shimapi.ContainerState_CONTAINER_RUNNING:
			state = "Running"
		case shimapi.ContainerState_CONTAINER_EXITED:
			state = "Exited"
		case shimapi.ContainerState_CONTAINER_UNKNOWN:
			state = "Unknown"
		default:
			state = "Invalid"
		}
		list += fmt.Sprintf("\n%v\t%v\t%v", container.Id, container.Image.Image, state)
	}
	return list
}

func containerStatus(client shimapi.RuntimeServiceClient) string {
	return "Not implemented" // TODO: Implement
}

func updateContainerResources(client shimapi.RuntimeServiceClient) string {
	return "Not implemented" // TODO: Implement
}

func execSync(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.ExecSyncRequest{}

	fmt.Print("Enter container ID: ")
	_, err := fmt.Scanln(&req.ContainerId)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter command: ")
	text, err := readLine()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	req.Cmd = quotedSplit(text)

	resp, err := client.ExecSync(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return "Stdout:\n" + string(resp.Stdout[:]) + "\nStderr:\n" + string(resp.Stderr[:])
}

func exec(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.ExecRequest{
		Stdin:  false,
		Stdout: true,
		Stderr: true,
	}

	fmt.Print("Enter container ID: ")
	_, err := fmt.Scanln(&req.ContainerId)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	fmt.Print("Enter command: ")
	text, err := readLine()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	req.Cmd = quotedSplit(text)

	fmt.Print("Forward stdin? (y/N): ")
	_, err = fmt.Scanln(&text)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if strings.ToLower(text) == "y" {
		req.Stdin = true
	}

	guid, err := guid.NewV4()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	pipe, err := winio.ListenPipe(`\\.\pipe\slclient\exec`+guid.String(), nil)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	var conn net.Conn
	ready := make(chan bool, 1)
	go func() {
		conn, err = pipe.Accept()
		if err != nil {
			fmt.Printf("Error: %v", err)
			ready <- false
		}
		ready <- true
	}()

	req.Pipe = pipe.Addr().String()
	_, err = client.Exec(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	readConnUntilSigint(conn, req.Stdin, ready)
	conn.Close()
	pipe.Close()

	return "Success"
}

// TODO: Attach only works once, then the gcs closes the
// log connections for some reason
func attach(client shimapi.RuntimeServiceClient) string {
	req := &shimapi.AttachRequest{
		Stdin:  false,
		Stdout: true,
		Stderr: true,
	}

	fmt.Print("Enter container ID: ")
	_, err := fmt.Scanln(&req.ContainerId)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	var text string
	fmt.Print("Forward stdin? (y/N): ")
	_, err = fmt.Scanln(&text)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if strings.ToLower(text) == "y" {
		req.Stdin = true
	} else {
		req.Stdin = false
	}

	guid, err := guid.NewV4()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	pipe, err := winio.ListenPipe(`\\.\pipe\slclient\attach`+guid.String(), nil)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	var conn net.Conn
	ready := make(chan bool, 1)
	go func() {
		conn, err = pipe.Accept()
		if err != nil {
			fmt.Printf("Error: %v", err)
			ready <- false
		}
		ready <- true
	}()

	req.Pipe = pipe.Addr().String()
	_, err = client.Attach(context.Background(), req)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	readConnUntilSigint(conn, req.Stdin, ready)
	conn.Close()
	pipe.Close()

	return "Success"
}
