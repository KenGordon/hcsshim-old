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

// InterruptModerationMode : The valid interrupt moderation modes for I/O virtualization (IOV) offloading.
type InterruptModerationMode string

// List of InterruptModerationMode
const (
	InterruptModerationMode_DEFAULT_ InterruptModerationMode = "Default"
	InterruptModerationMode_ADAPTIVE InterruptModerationMode = "Adaptive"
	InterruptModerationMode_OFF      InterruptModerationMode = "Off"
	InterruptModerationMode_LOW      InterruptModerationMode = "Low"
	InterruptModerationMode_MEDIUM   InterruptModerationMode = "Medium"
	InterruptModerationMode_HIGH     InterruptModerationMode = "High"
)
