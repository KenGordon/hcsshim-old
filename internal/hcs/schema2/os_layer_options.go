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

type OsLayerOptions struct {
	Type_                      *OsLayerType `json:"Type,omitempty"`
	DisableCiCacheOptimization bool         `json:"DisableCiCacheOptimization,omitempty"`
	SkipUpdateBcdForBoot       bool         `json:"SkipUpdateBcdForBoot,omitempty"`
	IsDynamic                  bool         `json:"IsDynamic,omitempty"`
	SkipSandboxPreExpansion    bool         `json:"SkipSandboxPreExpansion,omitempty"`
	FileSystemLayers           []Layer      `json:"FileSystemLayers,omitempty"`
	SandboxVhdPartitionId      string       `json:"SandboxVhdPartitionId,omitempty"`
}
