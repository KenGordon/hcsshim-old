package main

import (
	"context"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/gcs"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
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

func readerPrinter(r io.Reader) {
	buf := make([]byte, 1024)
	for {
		r.Read(buf)
		println(string(buf))
	}
}

// acceptGcs accepts and returns a connection from the UVM's GCS port
func acceptGcs(runtimeID string, port uint32) (*gcs.GuestConnection, error) {
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
	logrus.Info("Accepted GCS connection from UVM")

	// Start the GCS protocol.

	var initGuestState *gcs.InitialGuestState
	gcc := &gcs.GuestConnectionConfig{
		Conn:           conn,
		Log:            logrus.NewEntry(logrus.StandardLogger()),
		IoListen:       gcs.HvsockIoListen(ID),
		InitGuestState: initGuestState,
	}
	return gcc.Connect(context.Background(), true)
}

// connectLog connects to the UVM's log port and prints the output to
// stdout
func connectLog(runtimeID string, port uint32) {
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

	readerPrinter(conn)
}

type linuxHostedSystem struct {
	SchemaVersion    *hcsschema.Version
	OciBundlePath    string
	OciSpecification *specs.Spec
	ScratchDirPath   string
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
	go connectLog(runtimeID, logPort)

	gc, err := acceptGcs(runtimeID, gcsPort)
	if err != nil {
		gc.Close()
		logrus.Fatal(err)
	}
	defer gc.Close()
	logrus.Info("Connected to GCS")

	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeAdd,
	}
	req.Settings = guestresource.LCOWMappedVirtualDisk{
		MountPath:        "/run/mounts/scsi/m0",
		Controller:       uint8(0),
		Lun:              uint8(1),
		ReadOnly:         true,
		Partition:        uint64(0),
		Encrypted:        false,
		Options:          []string{},
		VerityInfo:       nil,
		EnsureFilesystem: true,
		Filesystem:       "ext4"}
	err = gc.Modify(context.Background(), req)
	if err != nil {
		logrus.Fatal(err)
	}

	req.Settings = guestresource.LCOWMappedVirtualDisk{
		MountPath:        "/run/mounts/scsi/m1",
		Controller:       uint8(0),
		Lun:              uint8(2),
		ReadOnly:         false,
		Partition:        uint64(0),
		Encrypted:        false,
		Options:          []string{},
		VerityInfo:       nil,
		EnsureFilesystem: true,
		Filesystem:       "ext4"}
	err = gc.Modify(context.Background(), req)
	if err != nil {
		logrus.Fatal(err)
	}

	req = guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeCombinedLayers,
		RequestType:  guestrequest.RequestTypeAdd,
		Settings: guestresource.LCOWCombinedLayers{
			ContainerID:       "alpine",
			ContainerRootPath: "/run/c/alpine/rootfs",
			Layers: []hcsschema.Layer{
				{Path: "/run/mounts/scsi/m0"},
			},
			ScratchPath: "/run/mounts/scsi/m1",
		},
	}
	err = gc.Modify(context.Background(), req)
	if err != nil {
		logrus.Fatal(err)
	}

	containerDoc := linuxHostedSystem{
		SchemaVersion: &hcsschema.Version{Major: 2, Minor: 1},
		OciBundlePath: "/run/gcs/c/alpine",
		OciSpecification: &specs.Spec{
			Version: "1.0.2-dev",
			Process: &specs.Process{
				User: specs.User{
					UID: 0,
					GID: 0,
				},
				Args: []string{"/bin/ash", "-c", "\"for i in $(seq 1 5); do echo $i; sleep 1; done\""},
				Cwd:  "/",
				Env:  []string{"TERM=xterm", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
			},
			Root: &specs.Root{
				Path: "/run/c/alpine/rootfs",
			},
			Linux: &specs.Linux{
				CgroupsPath: "/sys/fs/cgroup",
				Namespaces: []specs.LinuxNamespace{
					{Type: "pid", Path: "/proc/91/ns/pid"},
					{Type: "ipc", Path: "/proc/91/ns/ipc"},
					{Type: "uts", Path: "/proc/91/ns/uts"},
					{Type: "mount"},
					{Type: "network", Path: "/proc/91/ns/net"},
				},
				MaskedPaths: []string{
					"/proc/acpi",
					"/proc/asound",
					"/proc/kcore",
					"/proc/keys",
					"/proc/latency_stats",
					"/proc/timer_list",
					"/proc/timer_stats",
					"/proc/sched_debug",
					"/sys/firmware",
					"/proc/scsi",
				},
				ReadonlyPaths: []string{
					"/proc/bus",
					"/proc/fs",
					"/proc/irq",
					"/proc/sys",
					"/proc/sysrq-trigger",
				},
			},
			Mounts: []specs.Mount{
				{Destination: "/proc", Type: "proc", Source: "proc", Options: []string{"nosuid", "noexec", "nodev"}},
				{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
				{Destination: "/dev/pts", Type: "devpts", Source: "devpts", Options: []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"}},
				{Destination: "/dev/shm", Type: "tmpfs", Source: "shm", Options: []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"}},
				{Destination: "/dev/mqueue", Type: "mqueue", Source: "mqueue", Options: []string{"nosuid", "noexec", "nodev"}},
				{Destination: "/sys", Type: "sysfs", Source: "sysfs", Options: []string{"nosuid", "noexec", "nodev"}},
				{Destination: "/sys/fs/cgroup", Type: "cgroup", Source: "cgroup", Options: []string{"nosuid", "noexec", "nodev", "relatime", "ro"}},
			},
		},
		ScratchDirPath: "/run/mounts/scsi/m1/scratch/alpine",
	}
	logrus.Info("Creating container")
	c, err := gc.CreateContainer(context.Background(), "alpine", containerDoc)
	if err != nil {
		logrus.Fatal(err)
	}
	defer c.Close()
	println("Starting container\n\n\n\n")
	err = c.Start(context.Background())
	if err != nil {
		logrus.Fatal(err)
	}
	/* command := cmd.Command(c, "ash", "-c", "for i in $(seq 1 5); do echo $i; sleep 1; done")
	buf, err := command.Output()
	if err != nil {
		logrus.Fatal(err)
	}
	println(string(buf)) */
	c.Wait()
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
