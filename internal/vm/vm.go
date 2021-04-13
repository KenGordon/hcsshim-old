package vm

import (
	"context"
	"errors"
	"net"

	"github.com/Microsoft/go-winio/pkg/guid"
)

var (
	ErrNotSupported         = errors.New("virtstack does not support the operation")
	ErrAlreadySet           = errors.New("field has already been set")
	ErrNotInPreCreatedState = errors.New("VM is not in pre-created state")
	ErrNotInCreatedState    = errors.New("VM is not in created state")
	ErrNotInRunningState    = errors.New("VM is not in running state")
)

type UVMSource interface {
	NewLinuxUVM(ctx context.Context, id string, owner string) (UVM, error)
	NewWindowsUVM(ctx context.Context, id string, owner string) (UVM, error)
}

const (
	HCS      = "hcs"
	RemoteVM = "remotevm"
)

// UVM is an abstraction around a lightweight virtual machine. It houses core lifecycle methods such as Create
// Start, and Stop and also several optional nested interfaces that can be used to determine what the virtual machine
// supports and to configure these resources.
type UVM interface {
	// ID will return a string identifier for the Utility VM.
	ID() string
	// State returns the current running state of the Utility VM. e.g. Created, Running, Terminated
	State() State
	// Create
	Create(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Wait() error
	MemoryManager
	ProcessorManager
	BootManager
	SCSIManager
	VPMemManager
	NetworkManager
	PCIManager
	VMSocketManager
}

type State uint8

const (
	StatePreCreated State = iota
	StateCreated
	StateRunning
	StateTerminated
	StatePaused
)

type SCSIDiskType uint8

const (
	SCSIDiskTypeVHD1 SCSIDiskType = iota
	SCSIDiskTypeVHDX
	SCSIDiskTypePassThrough
)

type ProcessorManager interface {
	SetProcessorCount(ctx context.Context, count uint32) error
}

type BootManager interface {
	SetUEFIBoot(ctx context.Context, dir string, path string, args string) error
	SetLinuxKernelDirectBoot(ctx context.Context, kernel string, initRD string, cmd string) error
}

type SCSIManager interface {
	AddSCSIController(ctx context.Context, id uint32) error
	AddSCSIDisk(ctx context.Context, controller uint32, lun uint32, path string, typ SCSIDiskType, readOnly bool) error
}

type VPMemImageFormat uint8

const (
	VPMemImageFormatVHD1 VPMemImageFormat = iota
	VPMemImageFormatVHDX
)

type VPMemManager interface {
	AddVPMemController(ctx context.Context, maximumDevices uint32, maximumSizeBytes uint64) error
	AddVPMemDevice(ctx context.Context, id uint32, path string, readOnly bool, imageFormat VPMemImageFormat) error
}

type NetworkManager interface {
	AddNIC(ctx context.Context, nicID guid.GUID, endpointID string, mac string) error
}

// Stub for now, don't know what we need for Linux
type PCIManager interface {
	AddDevice(ctx context.Context) error
}

type VMSocketManager interface {
	HVSocketListen(ctx context.Context, serviceID guid.GUID) (net.Listener, error)
	VSockListen(ctx context.Context, port uint32) (net.Listener, error)
}

type MemoryManager interface {
	SetMemoryLimit(ctx context.Context, memoryMB uint64) error
	SetMemoryConfig(ctx context.Context, config *MemoryConfig) error
	SetMMIOConfig(ctx context.Context, lowGapMB uint64, highBaseMB uint64, highGapMB uint64) error
}

type MemoryBackingType uint8

const (
	MemoryBackingTypeVirtual MemoryBackingType = iota
	MemoryBackingTypePhysical
)

type MemoryConfig struct {
	BackingType     MemoryBackingType
	DeferredCommit  bool
	HotHint         bool
	ColdHint        bool
	ColdDiscardHint bool
}
