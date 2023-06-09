package main

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/gcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var (
	usage string = "./shimlike [runtimeID] [stdio port] [gcs port]"
)

type lcowProcessParameters struct {
	hcsschema.ProcessParameters
	OCIProcess *specs.Process `json:"OciProcess,omitempty"`
}

// acceptGcs accepts a connection from the UVM's GCS port
func acceptGcs(runtimeID string, port uint32) {
	ID, err := guid.FromString(runtimeID)
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Accepting GCS connection from UVM %s:%d", ID, port)
	listener, err := winio.ListenHvsock(&winio.HvsockAddr{
		VMID:      ID,
		ServiceID: winio.VsockServiceID(port),
	})
	if err != nil {
		logrus.Fatal(err)
	}
	defer listener.Close()
	conn, err := listener.Accept()
	if err != nil {
		logrus.Fatal(err)
	}
	defer conn.Close()
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
		gc.Close()
		logrus.Fatal(err)
	}
	defer gc.Close()
	logrus.Info("Connected to GCS")
	p, err := gc.CreateProcess(context.Background(), lcowProcessParameters{
		hcsschema.ProcessParameters{
			CommandLine:      "/bin/sh -c \"for i in $(seq 0 5); do ls /dev/; sleep 1; done\"",
			CreateStdOutPipe: true,
			CreateStdInPipe:  true,
			CreateStdErrPipe: true,
		},
		&specs.Process{},
	})
	if err != nil {
		logrus.Fatal(err)
	}
	defer p.Close()
	_, stdout, _ := p.Stdio()
	go func() {
		buf := make([]byte, 4096)
		for {
			stdout.Read(buf)
			print(string(buf))
		}
	}()
	/* input := ""
	for {
		fmt.Scanln(&input)
		stdin.Write([]byte(input))
	} */
	p.Wait()
}

// connectStdout connects to the UVM's stdout port and prints the output to
// stdout
func connectStdout(runtimeID string, port uint32) {
	ID, err := guid.FromString(runtimeID)
	if err != nil {
		logrus.Fatal(err)
	}
	logrus.Infof("Connecting to UVM %s:%d", ID, port)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := winio.Dial(timeoutCtx, &winio.HvsockAddr{
		VMID:      ID,
		ServiceID: winio.VsockServiceID(port),
	})
	if err != nil {
		logrus.Fatal(err)
	}
	defer conn.Close()
	logrus.Info("Connected to UVM")
	buf := make([]byte, 4096)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			logrus.Fatal(err)
		}
		println(string(buf))
	}
}

func run(cCtx *cli.Context) {
	if cCtx.NArg() != 3 {
		logrus.Fatal(usage)
	}
	runtimeID := cCtx.Args().First()
	port64, err := strconv.ParseUint(cCtx.Args().Get(1), 10, 32)
	if err != nil {
		logrus.Fatal(err)
	}
	logPort := uint32(port64)
	port64, err = strconv.ParseUint(cCtx.Args().Get(2), 10, 32)
	if err != nil {
		logrus.Fatal(err)
	}
	gcsPort := uint32(port64)
	go connectStdout(runtimeID, logPort)
	acceptGcs(runtimeID, gcsPort)

}

func main() {
	app := cli.App{
		Name:   "shimlike",
		Usage:  "Connect to a UVM",
		Action: run,
	}
	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
