package transport

import (
	"net"

	"github.com/Microsoft/go-winio/pkg/guid"
)

// IoListenFunc is a type for a function that creates a listener for a VM for
// the vsock port `port`.
type IoListenFunc func(port uint32) (net.Listener, error)

// WindowsGcsHvsockServiceID is the hvsock service ID that the Windows GCS
// will connect to.
var WindowsGcsHvsockServiceID = guid.GUID{
	Data1: 0xacef5661,
	Data2: 0x84a1,
	Data3: 0x4e44,
	Data4: [8]uint8{0x85, 0x6b, 0x62, 0x45, 0xe6, 0x9f, 0x46, 0x20},
}

// WindowsGcsHvHostID is the hvsock address for the parent of the VM running the GCS
var WindowsGcsHvHostID = guid.GUID{
	Data1: 0x894cc2d6,
	Data2: 0x9d79,
	Data3: 0x424f,
	Data4: [8]uint8{0x93, 0xfe, 0x42, 0x96, 0x9a, 0xe6, 0xd8, 0xd1},
}

// LinuxGcsVsockPort is the vsock port number that the Linux GCS will
// connect to.
const LinuxGcsVsockPort = 0x40000000

// katiewasnothere: took this directly from go-winion, figure out somethign better here
// VsockServiceID returns an hvsock service ID corresponding to the specified AF_VSOCK port.
func HvsockServiceIDFromPort(port uint32) guid.GUID {
	g, _ := guid.FromString("00000000-facb-11e6-bd58-64006a7986d3")
	g.Data1 = port
	return g
}
