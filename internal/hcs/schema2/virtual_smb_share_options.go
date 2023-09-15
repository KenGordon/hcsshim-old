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

type VirtualSmbShareOptions struct {
	ReadOnly bool `json:"ReadOnly,omitempty"`
	// convert exclusive access to shared read access
	ShareRead bool `json:"ShareRead,omitempty"`
	// all opens will use cached I/O
	CacheIo bool `json:"CacheIo,omitempty"`
	// disable oplock support
	NoOplocks bool `json:"NoOplocks,omitempty"`
	// Acquire the backup privilege when attempting to open
	TakeBackupPrivilege bool `json:"TakeBackupPrivilege,omitempty"`
	// Use the identity of the share root when opening
	UseShareRootIdentity bool `json:"UseShareRootIdentity,omitempty"`
	// disable Direct Mapping
	NoDirectmap bool `json:"NoDirectmap,omitempty"`
	// disable Byterange locks
	NoLocks bool `json:"NoLocks,omitempty"`
	// disable Directory CHange Notifications
	NoDirnotify bool `json:"NoDirnotify,omitempty"`
	// test mode
	Test bool `json:"Test,omitempty"`
	// share is use for VM shared memory
	VmSharedMemory bool `json:"VmSharedMemory,omitempty"`
	// allow access only to the files specified in AllowedFiles
	RestrictFileAccess bool `json:"RestrictFileAccess,omitempty"`
	// disable all oplocks except Level II
	ForceLevelIIOplocks bool `json:"ForceLevelIIOplocks,omitempty"`
	// Allow the host to reparse this base layer
	ReparseBaseLayer bool `json:"ReparseBaseLayer,omitempty"`
	// Enable pseudo-oplocks
	PseudoOplocks bool `json:"PseudoOplocks,omitempty"`
	// All opens will use non-cached IO
	NonCacheIo bool `json:"NonCacheIo,omitempty"`
	// Enable pseudo directory change notifications
	PseudoDirnotify bool `json:"PseudoDirnotify,omitempty"`
	// Content indexing disabled by the host for all files in the share
	DisableIndexing bool `json:"DisableIndexing,omitempty"`
	// Alternate data streams hidden to the guest (open fails, streams are not enumerated, etc.)
	HideAlternateDataStreams bool `json:"HideAlternateDataStreams,omitempty"`
	// Only FSCTLs listed in AllowedFsctls are allowed against any files in the share
	EnableFsctlFiltering bool `json:"EnableFsctlFiltering,omitempty"`
	// allow access only to the files specified in AllowedFiles, plus allow creation of new files.
	AllowNewCreates bool `json:"AllowNewCreates,omitempty"`
	// Block directory enumeration, renames, and deletes.
	SingleFileMapping bool `json:"SingleFileMapping,omitempty"`
	// Support Cloud Files functionality
	SupportCloudFiles bool `json:"SupportCloudFiles,omitempty"`
	// Filter EFS attributes from the guest
	FilterEncryptionAttributes bool `json:"FilterEncryptionAttributes,omitempty"`
}
