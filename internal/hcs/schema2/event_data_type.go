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

// EventDataType : Data types for event data elements, based on EVT_VARIANT_TYPE
type EventDataType string

// List of EventDataType
const (
	EventDataType_EMPTY       EventDataType = "Empty"
	EventDataType_STRING_     EventDataType = "String"
	EventDataType_ANSI_STRING EventDataType = "AnsiString"
	EventDataType_S_BYTE      EventDataType = "SByte"
	EventDataType_BYTE_       EventDataType = "Byte"
	EventDataType_INT16_      EventDataType = "Int16"
	EventDataType_U_INT16     EventDataType = "UInt16"
	EventDataType_INT32_      EventDataType = "Int32"
	EventDataType_U_INT32     EventDataType = "UInt32"
	EventDataType_INT64_      EventDataType = "Int64"
	EventDataType_U_INT64     EventDataType = "UInt64"
	EventDataType_SINGLE      EventDataType = "Single"
	EventDataType_DOUBLE      EventDataType = "Double"
	EventDataType_BOOLEAN     EventDataType = "Boolean"
	EventDataType_BINARY      EventDataType = "Binary"
	EventDataType_GUID        EventDataType = "Guid"
)
