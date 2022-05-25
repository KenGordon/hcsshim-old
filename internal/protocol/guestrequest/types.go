package guestrequest

// These are constants for v2 schema modify requests.

type RequestType string
type ResourceType string

// RequestType const
const (
	RequestTypeAdd    RequestType = "Add"
	RequestTypeRemove RequestType = "Remove"
	RequestTypePreAdd RequestType = "PreAdd" // For networking
	RequestTypeUpdate RequestType = "Update"
)

type SignalValueWCOW string

const (
	SignalValueWCOWCtrlC        SignalValueWCOW = "CtrlC"
	SignalValueWCOWCtrlBreak    SignalValueWCOW = "CtrlBreak"
	SignalValueWCOWCtrlClose    SignalValueWCOW = "CtrlClose"
	SignalValueWCOWCtrlLogOff   SignalValueWCOW = "CtrlLogOff"
	SignalValueWCOWCtrlShutdown SignalValueWCOW = "CtrlShutdown"
)

// ModificationRequest is for modify commands passed to the guest.
type ModificationRequest struct {
	RequestType  RequestType  `json:"RequestType,omitempty"`
	ResourceType ResourceType `json:"ResourceType,omitempty"`
	Settings     interface{}  `json:"Settings,omitempty"`
}

type NetworkModifyRequest struct {
	AdapterId   string      `json:"AdapterId,omitempty"` //nolint:stylecheck
	RequestType RequestType `json:"RequestType,omitempty"`
	Settings    interface{} `json:"Settings,omitempty"`
}

type RS4NetworkModifyRequest struct {
	AdapterInstanceId string      `json:"AdapterInstanceId,omitempty"` //nolint:stylecheck
	RequestType       RequestType `json:"RequestType,omitempty"`
	Settings          interface{} `json:"Settings,omitempty"`
}

var (
	// V5 GUIDs for SCSI controllers
	// These GUIDs are created with namespace GUID "d422512d-2bf2-4752-809d-7b82b5fcb1b4"
	// and index as names. For example, first GUID is created like this:
	// guid.NewV5("d422512d-2bf2-4752-809d-7b82b5fcb1b4", []byte("0"))
	ScsiControllerGuids = []string{
		// "df6d0690-79e5-55b6-a5ec-c1e2f77f580a",
		// "0110f83b-de10-5172-a266-78bca56bf50a",
		// "b5d2d8d4-3a75-51bf-945b-3444dc6b8579",
		// "305891a9-b251-5dfe-91a2-c25d9212275b",
		"adad1ba1-facb-11e6-bd58-64006a7986d3", // TODO katiewasnothere: these are hvlite guids
		"adad1ba4-facb-11e6-bd58-64006a7986d3",
		"adad1ba2-facb-11e6-bd58-64006a7986d3",
		"ba6163d9-04a1-4d29-b605-72e2ffb1dc7f",
		"adad1ba3-facb-11e6-bd58-64006a7986d3",
		"c6aa4143-4bd5-4d36-b8ec-713a449d159a",
	}
)
