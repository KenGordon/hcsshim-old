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

type RegistryValueType string

// List of RegistryValueType
const (
	RegistryValueType_NONE            RegistryValueType = "None"
	RegistryValueType_STRING_         RegistryValueType = "String"
	RegistryValueType_EXPANDED_STRING RegistryValueType = "ExpandedString"
	RegistryValueType_MULTI_STRING    RegistryValueType = "MultiString"
	RegistryValueType_BINARY          RegistryValueType = "Binary"
	RegistryValueType_D_WORD          RegistryValueType = "DWord"
	RegistryValueType_Q_WORD          RegistryValueType = "QWord"
	RegistryValueType_CUSTOM_TYPE     RegistryValueType = "CustomType"
)
