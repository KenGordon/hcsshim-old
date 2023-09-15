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

type SharedMemoryRegion struct {
	SectionName     string `json:"SectionName,omitempty"`
	StartOffset     uint64 `json:"StartOffset,omitempty"`
	Length          uint64 `json:"Length,omitempty"`
	AllowGuestWrite bool   `json:"AllowGuestWrite,omitempty"`
	HiddenFromGuest bool   `json:"HiddenFromGuest,omitempty"`
}
