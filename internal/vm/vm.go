package vm

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio/pkg/guid"
)

type UVMSource interface {
	NewLinuxUVM(id string, owner string) (UVM, error)
}

type UVM interface {
	ID() string
	State() State
	Create(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Wait() error
}

type State uint8

const (
	StatePreCreated State = iota
	StateCreated
	StateRunning
	StateTerminated
)

type MemoryControl interface {
	SetMemoryLimit(ctx context.Context, memoryMB uint64) error
}

type MemoryConfigControl interface {
	SetMemoryConfig(ctx context.Context, config *MemoryConfig) error
}

type MemoryConfig struct {
	BackingType     MemoryBackingType
	DeferredCommit  bool
	HotHint         bool
	ColdHint        bool
	ColdDiscardHint bool
}

type MemoryBackingType uint8

const (
	MemoryBackingTypeVirtual MemoryBackingType = iota
	MemoryBackingTypePhysical
)

type MMIOConfigControl interface {
	SetMMIOConfig(ctx context.Context, lowGapMB uint64, highBaseMB uint64, highGapMB uint64) error
}

type ProcessorControl interface {
	SetProcessorCount(ctx context.Context, count uint64) error
}

type SCSI interface {
	AddSCSIController(ctx context.Context, id uint32) error
	AddSCSIDisk(ctx context.Context, controller uint32, lun uint32, path string, typ SCSIDiskType, readOnly bool) error
}

type SCSIDiskType uint8

const (
	SCSIDiskTypeVHD1 SCSIDiskType = iota
	SCSIDiskTypeVHDX
	SCSIDiskTypePassThrough
)

type VPMem interface {
	AddVPMemController(ctx context.Context, maximumDevices uint32, maximumSizeBytes uint64) error
	AddVPMemDevice(ctx context.Context, id uint32, path string, readOnly bool, imageFormat VPMemImageFormat) error
}

type VPMemImageFormat uint8

const (
	VPMemImageFormatVHD1 VPMemImageFormat = iota
	VPMemImageFormatVHDX
)

type UEFIBoot interface {
	SetUEFIBoot(ctx context.Context, dir string, path string, args string) error
}

type LinuxBoot interface {
	SetLinuxKernelDirectBoot(ctx context.Context, kernel string, initRD string, cmd string) error
}

type HVSocketListen interface {
	HVSocketListen(ctx context.Context, serviceID guid.GUID) (net.Listener, error)
}

type Network interface {
	AddNIC(ctx context.Context, nicID guid.GUID, endpointID string, mac string) error
}
