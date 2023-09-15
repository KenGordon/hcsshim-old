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

type WorkerExitType string

// List of WorkerExitType
const (
	WorkerExitType_NONE                  WorkerExitType = "None"
	WorkerExitType_INITIALIZATION_FAILED WorkerExitType = "InitializationFailed"
	WorkerExitType_STOPPED               WorkerExitType = "Stopped"
	WorkerExitType_SAVED                 WorkerExitType = "Saved"
	WorkerExitType_STOPPED_ON_RESET      WorkerExitType = "StoppedOnReset"
	WorkerExitType_UNEXPECTED_STOP       WorkerExitType = "UnexpectedStop"
	WorkerExitType_RESET_FAILED          WorkerExitType = "ResetFailed"
	WorkerExitType_UNRECOVERABLE_ERROR   WorkerExitType = "UnrecoverableError"
)
