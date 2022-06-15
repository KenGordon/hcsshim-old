//go:build windows

package main

import (
	"errors"

	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/Microsoft/hcsshim/internal/hcs"
	"github.com/containerd/containerd/errdefs"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

var (
	errTaskNotIsolated              = errors.New("task is not isolated")
	errNotSupportedResourcesRequest = errors.New("update resources must be of type *WindowsResources or *LinuxResources")
)

func verifyTaskUpdateResourcesType(data interface{}) error {
	switch data.(type) {
	case *specs.WindowsResources:
	case *specs.LinuxResources:
	default:
		return errNotSupportedResourcesRequest
	}
	return nil
}

// isStatsNotFound returns true if the err corresponds to a scenario
// where statistics cannot be retrieved or found
func isStatsNotFound(err error) bool {
	return errdefs.IsNotFound(err) ||
		hcs.IsNotExist(err) ||
		hcs.IsOperationInvalidState(err) ||
		gcs.IsNotExist(err) ||
		hcs.IsAccessIsDenied(err) ||
		hcs.IsErrorInvalidHandle(err)
}
