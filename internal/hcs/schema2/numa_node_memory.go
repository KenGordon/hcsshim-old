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

type NumaNodeMemory struct {
	// Total physical memory on on this physical NUMA node that is consumable by the VMs.
	TotalConsumableMemoryInPages uint64 `json:"TotalConsumableMemoryInPages,omitempty"`
	// Currently available physical memory on this physical NUMA node for the VMs.
	AvailableMemoryInPages uint64 `json:"AvailableMemoryInPages,omitempty"`
}
