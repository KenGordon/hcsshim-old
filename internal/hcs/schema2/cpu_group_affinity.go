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

type CpuGroupAffinity struct {
	LogicalProcessorCount uint32  `json:"LogicalProcessorCount,omitempty"`
	LogicalProcessors     []int64 `json:"LogicalProcessors,omitempty"`
}
