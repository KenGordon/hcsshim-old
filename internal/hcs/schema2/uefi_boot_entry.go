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

type UefiBootEntry struct {
	DeviceType    *UefiBootDevice `json:"DeviceType,omitempty"`
	DevicePath    string          `json:"DevicePath,omitempty"`
	DiskNumber    uint16          `json:"DiskNumber,omitempty"`
	OptionalData  string          `json:"OptionalData,omitempty"`
	VmbFsRootPath string          `json:"VmbFsRootPath,omitempty"`
}
