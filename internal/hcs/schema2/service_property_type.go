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

// ServicePropertyType : legacy service property types New implementation uses GetPropertyType and ModifyPropertyType
type ServicePropertyType string

// List of Service_PropertyType
const (
	ServicePropertyType_BASIC                      ServicePropertyType = "Basic"
	ServicePropertyType_MEMORY                     ServicePropertyType = "Memory"
	ServicePropertyType_CPU_GROUP                  ServicePropertyType = "CpuGroup"
	ServicePropertyType_PROCESSOR_TOPOLOGY         ServicePropertyType = "ProcessorTopology"
	ServicePropertyType_CACHE_ALLOCATION           ServicePropertyType = "CacheAllocation"
	ServicePropertyType_CACHE_MONITORING           ServicePropertyType = "CacheMonitoring"
	ServicePropertyType_CONTAINER_CREDENTIAL_GUARD ServicePropertyType = "ContainerCredentialGuard"
	ServicePropertyType_QO_S_CAPABILITIES          ServicePropertyType = "QoSCapabilities"
	ServicePropertyType_MEMORY_BW_ALLOCATION       ServicePropertyType = "MemoryBwAllocation"
	ServicePropertyType_PROCESSOR                  ServicePropertyType = "Processor"
	ServicePropertyType_SECURE_NESTED_PAGING       ServicePropertyType = "SecureNestedPaging"
	ServicePropertyType_UNDEFINED                  ServicePropertyType = "Undefined"
)
