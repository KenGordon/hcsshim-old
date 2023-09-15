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

// ModifyServiceOperation : Enumeration of different supported service processor modification requests
type ModifyServiceOperation string

// List of ModifyServiceOperation
const (
	ModifyServiceOperation_CREATE_GROUP ModifyServiceOperation = "CreateGroup"
	ModifyServiceOperation_DELETE_GROUP ModifyServiceOperation = "DeleteGroup"
	ModifyServiceOperation_SET_PROPERTY ModifyServiceOperation = "SetProperty"
)
