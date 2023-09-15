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

type MappedVirtualDisk struct {
	ContainerPath string `json:"ContainerPath,omitempty"`
	Lun           uint8  `json:"Lun,omitempty"`
	// If `true` then delete `ContainerPath` if it exists.
	OverwriteIfExists bool `json:"OverwriteIfExists,omitempty"`
}
