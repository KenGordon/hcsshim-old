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

// Object that describes a named pipe that is requested to be mapped into a compute system's guest.
type MappedPipe struct {
	// The resulting named pipe that will be accessible in the compute system's guest.
	ContainerPipeName string `json:"ContainerPipeName,omitempty"`
	// The named pipe path in the host that will be mapped into a compute system's guest.
	HostPath     string              `json:"HostPath,omitempty"`
	HostPathType *MappedPipePathType `json:"HostPathType,omitempty"`
}
