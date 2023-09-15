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

// Document provided in the EventData parameter of an HcsEventSystemExited HCS_EVENT.
type SystemExitStatus struct {
	// Exit status (HRESULT) for the system.
	Status      int32               `json:"Status,omitempty"`
	ExitType    *NotificationType   `json:"ExitType,omitempty"`
	Attribution []AttributionRecord `json:"Attribution,omitempty"`
}
