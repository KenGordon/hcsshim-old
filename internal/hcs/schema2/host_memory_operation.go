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

type HostMemoryOperation string

// List of HostMemoryOperation
const (
	HostMemoryOperation_MEMORY_RESERVE_GROW             HostMemoryOperation = "MemoryReserveGrow"
	HostMemoryOperation_MEMORY_RESERVE_SHRINK           HostMemoryOperation = "MemoryReserveShrink"
	HostMemoryOperation_MEMORY_BALANCING_ENABLE         HostMemoryOperation = "MemoryBalancingEnable"
	HostMemoryOperation_MEMORY_RESERVE_TARGET           HostMemoryOperation = "MemoryReserveTarget"
	HostMemoryOperation_MEMORY_RESERVE_IO_SPACE_CONVERT HostMemoryOperation = "MemoryReserveIoSpaceConvert"
	HostMemoryOperation_MEMORY_RESERVE_IO_SPACE_GROW    HostMemoryOperation = "MemoryReserveIoSpaceGrow"
	HostMemoryOperation_MEMORY_RESERVE_IO_SPACE_SHRINK  HostMemoryOperation = "MemoryReserveIoSpaceShrink"
)
