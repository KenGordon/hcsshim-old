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

type HostMemoryModificationRequest struct {
	Operation        *HostMemoryOperation `json:"Operation,omitempty"`
	OperationDetails interface{}          `json:"OperationDetails,omitempty"`
}
