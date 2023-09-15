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

// Sgx refers to Intel Software Guard Extension. This property allows a fixed amount of enclave page cache memory allocated on the host to be mapped to the guest TODO: might be removed as legacy settings
type SgxMemory struct {
	// Enablement of Sgx memory support in VM
	Enabled bool `json:"Enabled,omitempty"`
	// The amount of enclave page cache memory to assign to the VM
	SizeInMB          uint64                `json:"SizeInMB,omitempty"`
	LaunchControlMode *SgxLaunchControlMode `json:"LaunchControlMode,omitempty"`
	// Flexible launch control mode's MSR values that the guest starts with. The specified MSR here is IA32_SGXLEPUBKEYHASH
	LaunchControlDefault string `json:"LaunchControlDefault,omitempty"`
}
