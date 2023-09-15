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

type VirtualPMemDevice struct {
	HostPath    string                        `json:"HostPath,omitempty"`
	ReadOnly    bool                          `json:"ReadOnly,omitempty"`
	ImageFormat *VirtualPMemImageFormat       `json:"ImageFormat,omitempty"`
	SizeBytes   uint64                        `json:"SizeBytes,omitempty"`
	Mappings    map[string]VirtualPMemMapping `json:"Mappings,omitempty"`
}
