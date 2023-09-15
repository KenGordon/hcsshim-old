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

type GuestModifySettingRequest struct {
	ResourceType *ModifyResourceType `json:"ResourceType,omitempty"`
	RequestType  *ModifyRequestType  `json:"RequestType,omitempty"`
	Settings     interface{}         `json:"Settings,omitempty"`
	// TODO (beweedon 7/6/2018): Remove this field once we no longer support RS4 GCS.
	HostedSettings interface{} `json:"HostedSettings,omitempty"`
}
