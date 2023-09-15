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

type OperationFailureDetail string

// List of OperationFailureDetail
const (
	OperationFailureDetail_INVALID                  OperationFailureDetail = "Invalid"
	OperationFailureDetail_CREATE_INTERNAL_ERROR    OperationFailureDetail = "CreateInternalError"
	OperationFailureDetail_CONSTRUCT_STATE_ERROR    OperationFailureDetail = "ConstructStateError"
	OperationFailureDetail_RUNTIME_OS_TYPE_MISMATCH OperationFailureDetail = "RuntimeOsTypeMismatch"
	OperationFailureDetail_CONSTRUCT                OperationFailureDetail = "Construct"
	OperationFailureDetail_START                    OperationFailureDetail = "Start"
	OperationFailureDetail_PAUSE                    OperationFailureDetail = "Pause"
	OperationFailureDetail_RESUME                   OperationFailureDetail = "Resume"
	OperationFailureDetail_SHUTDOWN                 OperationFailureDetail = "Shutdown"
	OperationFailureDetail_TERMINATE                OperationFailureDetail = "Terminate"
	OperationFailureDetail_SAVE                     OperationFailureDetail = "Save"
	OperationFailureDetail_GET_PROPERTIES           OperationFailureDetail = "GetProperties"
	OperationFailureDetail_MODIFY                   OperationFailureDetail = "Modify"
	OperationFailureDetail_CRASH                    OperationFailureDetail = "Crash"
	OperationFailureDetail_GUEST_CRASH              OperationFailureDetail = "GuestCrash"
	OperationFailureDetail_LIFECYCLE_NOTIFY         OperationFailureDetail = "LifecycleNotify"
	OperationFailureDetail_EXECUTE_PROCESS          OperationFailureDetail = "ExecuteProcess"
	OperationFailureDetail_GET_PROCESS_INFO         OperationFailureDetail = "GetProcessInfo"
	OperationFailureDetail_WAIT_FOR_PROCESS         OperationFailureDetail = "WaitForProcess"
	OperationFailureDetail_SIGNAL_PROCESS           OperationFailureDetail = "SignalProcess"
	OperationFailureDetail_MODIFY_PROCESS           OperationFailureDetail = "ModifyProcess"
	OperationFailureDetail_PREPARE_FOR_HOSTING      OperationFailureDetail = "PrepareForHosting"
	OperationFailureDetail_REGISTER_HOSTED_SYSTEM   OperationFailureDetail = "RegisterHostedSystem"
	OperationFailureDetail_UNREGISTER_HOSTED_SYSTEM OperationFailureDetail = "UnregisterHostedSystem"
	OperationFailureDetail_PREPARE_FOR_CLONE        OperationFailureDetail = "PrepareForClone"
	OperationFailureDetail_GET_CLONE_TEMPLATE       OperationFailureDetail = "GetCloneTemplate"
)
