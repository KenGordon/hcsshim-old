//go:build !windows
// +build !windows

package shimdiag

import (
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/ttrpc"
)

func FindShims(name string) ([]string, error) {
	return nil, errdefs.ErrNotImplemented
}

func GetShim(name string) (*ttrpc.Client, error) {
	return nil, errdefs.ErrNotImplemented
}
