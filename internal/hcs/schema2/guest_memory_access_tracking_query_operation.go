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

type GuestMemoryAccessTrackingQueryOperation struct {
	PageIndex    uint64                         `json:"PageIndex,omitempty"`
	TrackingType *GuestMemoryAccessTrackingType `json:"TrackingType,omitempty"`
}
