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

type VirtualPMemController struct {
	Devices map[string]VirtualPMemDevice `json:"Devices,omitempty"`
	// This field indicates how many empty devices to add to the controller. If non-zero, additional VirtualPMemDevice objects with no HostPath and no Mappings will be added to the Devices map to get up to the MaximumCount. These devices will be configured with either the MaximumSizeBytes field if non-zero, or with the default maximum, 512 Mb.
	MaximumCount     uint8                   `json:"MaximumCount,omitempty"`
	MaximumSizeBytes uint64                  `json:"MaximumSizeBytes,omitempty"`
	Backing          *VirtualPMemBackingType `json:"Backing,omitempty"`
}
