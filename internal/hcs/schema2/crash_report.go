// Autogenerated code; DO NOT EDIT.

/*
 * Schema Open API
 *
 * No description provided (generated by Swagger Codegen https://github.com/swagger-api/swagger-codegen)
 *
 * API version: 2.4
 * Contact: containerplat-dev@microsoft.com
 * Generated by: Swagger Codegen (https://github.com/swagger-api/swagger-codegen.git)
 */

package hcsschema

// Crash information reported through HcsEventSystemCrashInitiated and HcsEventSystemCrashReport notifications. This object is also used as the input to HcsSubmitWerReport.
type CrashReport struct {
	// Compute system id the CrashReport is for.
	SystemId string `json:"SystemId,omitempty"`
	// Trace correlation activity Id.
	ActivityId       string              `json:"ActivityId,omitempty"`
	WindowsCrashInfo *WindowsCrashReport `json:"WindowsCrashInfo,omitempty"`
	// Crash parameters as reported by the guest OS. For Windows these correspond to the bug check code followed by 4 bug check code specific values. The CrashParameters are available in both HcsEventSystemCrashInitiated and HcsEventSystemCrashReport events.
	CrashParameters []int64 `json:"CrashParameters,omitempty"`
	// An optional string provided by the guest OS. Currently only used by Linux guest OSes with Hyper-V Linux Integration Services configured.
	CrashLog string                  `json:"CrashLog,omitempty"`
	VmwpDump *CrashReportProcessDump `json:"VmwpDump,omitempty"`
	LiveDump *CrashReportLiveDump    `json:"LiveDump,omitempty"`
	// Provides overall status on crash reporting, S_OK indicates success, other HRESULT values on error.
	Status int32 `json:"Status,omitempty"`
	// Opaque guest OS reported ID.
	PreOSId uint32 `json:"PreOSId,omitempty"`
	// If true, the guest OS reported that a crash dump stack/handler was unavailable or could not be invoked.
	CrashStackUnavailable bool `json:"CrashStackUnavailable,omitempty"`
}
