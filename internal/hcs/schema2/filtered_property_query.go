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

// Structures used to perform a filtered property query.
type FilteredPropertyQuery struct {
	PropertyType *GetPropertyType `json:"PropertyType,omitempty"`
	// Filter - Additional filter to query. The following map describes the relationship between property type and its filter. [\"Memory\" => HostMemoryQueryRequest]
	Filter interface{} `json:"Filter,omitempty"`
}
