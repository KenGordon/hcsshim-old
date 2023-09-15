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

import (
	"time"
)

// Information about a process running in a container
type ProcessDetails struct {
	ProcessId                    uint32    `json:"ProcessId,omitempty"`
	ImageName                    string    `json:"ImageName,omitempty"`
	CreateTimestamp              time.Time `json:"CreateTimestamp,omitempty"`
	UserTime100ns                uint64    `json:"UserTime100ns,omitempty"`
	KernelTime100ns              uint64    `json:"KernelTime100ns,omitempty"`
	MemoryCommitBytes            uint64    `json:"MemoryCommitBytes,omitempty"`
	MemoryWorkingSetPrivateBytes uint64    `json:"MemoryWorkingSetPrivateBytes,omitempty"`
	MemoryWorkingSetSharedBytes  uint64    `json:"MemoryWorkingSetSharedBytes,omitempty"`
}
